package sshmuxer

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/antoniomika/sish/utils"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

const (
	// commandSplitter is the character that terminates a prefix.
	commandSplitter = "="

	// proxyProtocolPrefix is used when deciding what proxy protocol
	// version to use.
	proxyProtocolPrefix = "proxy-protocol"

	// proxyProtoPrefixLegacy is used when deciding what proxy protocol
	// version to use.
	proxyProtoPrefixLegacy = "proxyproto"

	// hostHeaderPrefix is the host-header for a specific session.
	hostHeaderPrefix = "host-header"

	// stripPathPrefix defines whether or not to strip the path (if enabled globally).
	stripPathPrefix = "strip-path"

	// sniProxyPrefix defines whether or not to enable SNI Proxying (if enabled globally).
	sniProxyPrefix = "sni-proxy"

	// tcpAliasPrefix defines whether or not to enable TCP Aliasing (if enabled globally).
	tcpAliasPrefix = "tcp-alias"

	// localForwardPrefix defines whether or not a local forward is being used (allows for logging).
	localForwardPrefix = "local-forward"

	// autoClosePrefix defines whether or not a connection will close when all forwards are cleaned up.
	autoClosePrefix = "auto-close"

	// forceHTTPSPrefix defines whether or not a connection will redirect to https.
	forceHTTPSPrefix = "force-https"

	// forceConnectPrefix defines whether or not to force takeover of an in-use target.
	forceConnectPrefix = "force-connect"

	// tcpAddressPrefix defines whether or not to set the tcp address for a tcp forward.
	tcpAddressPrefix = "tcp-address"

	// tcpAliasesAllowedUsersPrefix defines a comma separated list of allowed key fingerprints to access TCP aliases.
	tcpAliasesAllowedUsersPrefix = "tcp-aliases-allowed-users"

	// deadlinePrefix defines a timestamp at which the connection will close automatically.
	deadlinePrefix = "deadline"

	// notePrefix defines a plain text note to attach to a connection.
	notePrefix = "note"

	// note64Prefix defines a base64-encoded note to attach to a connection.
	note64Prefix = "note64"

	// idPrefix defines a custom connection identifier.
	idPrefix = "id"

	// noteEnvVar defines the env var used for plain text connection notes.
	noteEnvVar = "SISH_NOTE"

	// note64EnvVar defines the env var used for base64-encoded connection notes.
	note64EnvVar = "SISH_NOTE64"
)

type envRequestPayload struct {
	Name  string
	Value string
}

var validConnectionIDRegex = regexp.MustCompile(`^[A-Za-z0-9._-]{1,50}$`)

// handleSession handles the channel when a user requests a session.
// This is how we send console messages.
func handleSession(newChannel ssh.NewChannel, sshConn *utils.SSHConnection, state *utils.State) {
	connection, requests, err := newChannel.Accept()
	if err != nil {
		sshConn.SetCloseInfo("server", "session channel accept failed")
		sshConn.CleanUp(state)
		return
	}

	if viper.GetBool("debug") {
		log.Println("Handling session for connection:", connection)
	}

	welcomeMessage := viper.GetString("welcome-message")
	if welcomeMessage != "" {
		writeToSession(connection, aurora.BgRed(welcomeMessage).String()+"\r\n")
	}

	go func() {
		for {
			select {
			case c := <-sshConn.Messages:
				writeToSession(connection, c)
			case <-sshConn.Close:
				return
			}
		}
	}()

	go func() {
		<-sshConn.Close

		if !sshConn.GetExecMode() {
			return
		}

		type exitStatusMsg struct {
			Status uint32
		}

		_, err := connection.SendRequest("exit-status", false, ssh.Marshal(&exitStatusMsg{Status: 255}))
		if err != nil && viper.GetBool("debug") {
			log.Println("Error sending exec exit status:", err)
		}
	}()

	go func() {
		for {
			data := make([]byte, 4096)
			dataRead, err := connection.Read(data)
			if err != nil && err == io.EOF {
				break
			} else if err != nil {
				select {
				case <-sshConn.Close:
					break
				default:
					sshConn.SetCloseInfo("client", "session read error")
					sshConn.CleanUp(state)
				}
				break
			}

			if dataRead != 0 {
				if data[0] == 3 {
					sshConn.SetCloseInfo("client", "interrupted (Ctrl+C)")
					sshConn.CleanUp(state)
				}
			}
		}
	}()

	go func() {
		sshConn.SetStripPath(viper.GetBool("strip-http-path"))

		for req := range requests {
			switch req.Type {
			case "env":
				envPayload := &envRequestPayload{}
				err := ssh.Unmarshal(req.Payload, envPayload)
				if err != nil {
					if req.WantReply {
						err = req.Reply(false, nil)
						if err != nil {
							log.Println("Error replying to env request:", err)
						}
					}
					log.Println("Error unmarshaling env payload:", err)
					break
				}

				if command, ok := envNameToCommand(envPayload.Name); ok {
					err = applyConnectionCommand(command, envPayload.Value, sshConn)
					if err != nil {
						if req.WantReply {
							err = req.Reply(false, nil)
							if err != nil {
								log.Println("Error replying to env request:", err)
							}
						}

						sshConn.SendMessage(fmt.Sprintf("Unable to apply %s: %s", command, err), true)
						sshConn.SetCloseInfo("server", "invalid connection command")
						sshConn.CleanUp(state)
						return
					}
				}

				if req.WantReply {
					err = req.Reply(true, nil)
					if err != nil {
						log.Println("Error replying to env request:", err)
					}
				}
			case "shell":
				err := req.Reply(true, nil)
				if err != nil {
					log.Println("Error replying to socket request:", err)
				}

				sshConn.CloseExecChannel()
			case "exec":
				err := req.Reply(true, nil)
				if err != nil {
					log.Println("Error replying to exec request:", err)
				}

				sshConn.SetExecMode(true)

				// The exec payload is an SSH string: a 4-byte length prefix
				// followed by the command. A client may send a shorter (or
				// empty) payload, which made the unguarded req.Payload[4:]
				// slice panic and take the whole process down (B9).
				payloadString := ""
				if len(req.Payload) > 4 {
					payloadString = string(req.Payload[4:])
				}
				commandFlags := coalesceNoteFlags(parseCommandFlags(payloadString))

				for _, commandFlag := range commandFlags {
					commandFlagParts := strings.SplitN(commandFlag, commandSplitter, 2)

					if len(commandFlagParts) < 2 {
						continue
					}

					command, param := commandFlagParts[0], commandFlagParts[1]

					err = applyConnectionCommand(command, param, sshConn)
					if err != nil {
						sshConn.SendMessage(fmt.Sprintf("Unable to apply %s: %s", command, err), true)
						sshConn.SetCloseInfo("server", "invalid connection command")
						sshConn.CleanUp(state)
						return
					}
				}

				sshConn.CloseExecChannel()
			default:
				if viper.GetBool("debug") {
					log.Println("Sub Channel Type", req.Type, req.WantReply, string(req.Payload))
				}
			}
		}
	}()
}

// envNameToCommand maps SISH_* environment variables to tunnel command names.
func envNameToCommand(name string) (string, bool) {
	if !strings.HasPrefix(name, "SISH_") {
		return "", false
	}

	command := strings.TrimPrefix(name, "SISH_")
	if command == "" {
		return "", false
	}

	command = strings.ToLower(strings.ReplaceAll(command, "_", "-"))
	return command, true
}

// applyConnectionCommand applies connection-level command options from exec/env inputs.
func applyConnectionCommand(command string, param string, sshConn *utils.SSHConnection) error {
	switch command {
	case proxyProtocolPrefix:
		fallthrough
	case proxyProtoPrefixLegacy:
		if !viper.GetBool("proxy-protocol") {
			return nil
		}
		proxyProtoVersion := getProxyProtoVersion(param)
		sshConn.SetProxyProto(proxyProtoVersion)
		if proxyProtoVersion != 0 {
			sshConn.SendMessage(fmt.Sprintf("Proxy protocol enabled for TCP connections. Using protocol version %d", int(proxyProtoVersion)), true)
		}
	case hostHeaderPrefix:
		if !viper.GetBool("rewrite-host-header") {
			return nil
		}
		sshConn.SetHostHeader(param)
		sshConn.SendMessage(fmt.Sprintf("Using host header %s for HTTP handlers", param), true)
	case stripPathPrefix:
		if !sshConn.GetStripPath() {
			return nil
		}

		nstripPath, err := strconv.ParseBool(param)

		if err != nil {
			log.Printf("Unable to detect strip path setting. Using configuration: %s", err)
		} else {
			sshConn.SetStripPath(nstripPath)
		}

		sshConn.SendMessage(fmt.Sprintf("Strip path for HTTP handlers set to: %t", sshConn.GetStripPath()), true)
	case sniProxyPrefix:
		if !viper.GetBool("sni-proxy") {
			return nil
		}

		sniProxy, err := strconv.ParseBool(param)

		if err != nil {
			log.Printf("Unable to detect sni proxy setting. Using false as default: %s", err)
		}

		sshConn.SetSNIProxy(sniProxy)

		sshConn.SendMessage(fmt.Sprintf("SNI proxy for TCP forwards set to: %t", sniProxy), true)
	case tcpAddressPrefix:
		if viper.GetBool("force-tcp-address") {
			return nil
		}

		sshConn.SetTCPAddress(param)

		sshConn.SendMessage(fmt.Sprintf("TCP address for TCP forwards set to: %s", param), true)
	case tcpAliasPrefix:
		if !viper.GetBool("tcp-aliases") {
			return nil
		}

		tcpAlias, err := strconv.ParseBool(param)

		if err != nil {
			log.Printf("Unable to detect tcp alias setting. Using false as default: %s", err)
		}

		sshConn.SetTCPAlias(tcpAlias)

		sshConn.SendMessage(fmt.Sprintf("TCP alias for TCP forwards set to: %t", tcpAlias), true)
	case autoClosePrefix:
		autoClose, err := strconv.ParseBool(param)

		if err != nil {
			log.Printf("Unable to detect auto close setting. Using false as default: %s", err)
		}

		sshConn.SetAutoClose(autoClose)

		sshConn.SendMessage(fmt.Sprintf("Auto close for connection set to: %t", autoClose), true)
	case forceHTTPSPrefix:
		if !viper.GetBool("force-https") {
			return nil
		}

		forceHTTPS, err := strconv.ParseBool(param)
		if err != nil {
			log.Printf("Unable to detect force https setting. Using false as default: %s", err)
		}
		sshConn.SetForceHTTPS(forceHTTPS)
		sshConn.SendMessage(fmt.Sprintf("Force https for connection set to: %t", forceHTTPS), true)
	case forceConnectPrefix:
		forceConnect, err := strconv.ParseBool(param)
		if err != nil {
			log.Printf("Unable to detect force connect setting. Using false as default: %s", err)
		}

		if forceConnect && !viper.GetBool("enable-force-connect") {
			sshConn.SetForceConnect(false)
			sshConn.SendMessage("Force connect requested, but server-side enable-force-connect is disabled. Ignoring setting.", true)
			break
		}

		sshConn.SetForceConnect(forceConnect)
		sshConn.SendMessage(fmt.Sprintf("Force connect for requested target set to: %t", forceConnect), true)
	case localForwardPrefix:
		localForward, err := strconv.ParseBool(param)

		if err != nil {
			log.Printf("Unable to detect tcp alias setting. Using false as default: %s", err)
		}

		sshConn.SetLocalForward(localForward)

		sshConn.SendMessage(fmt.Sprintf("Connection used for local forwards set to: %t", localForward), true)
	case tcpAliasesAllowedUsersPrefix:
		if !viper.GetBool("tcp-aliases-allowed-users") {
			return nil
		}

		fingerPrints := strings.Split(param, ",")

		for i, fingerPrint := range fingerPrints {
			fingerPrints[i] = strings.TrimSpace(fingerPrint)
		}

		connPubKey := ""
		if sshConn.SSHConn.Permissions != nil {
			if _, ok := sshConn.SSHConn.Permissions.Extensions["pubKey"]; ok {
				connPubKey = sshConn.SSHConn.Permissions.Extensions["pubKeyFingerprint"]
			}
		}

		allowedUsers := fingerPrints

		printKeys := fingerPrints
		if connPubKey != "" {
			allowedUsers = append(slices.Clone(allowedUsers), connPubKey)
			printKeys = slices.Insert(printKeys, 0, fmt.Sprintf("%s (self)", connPubKey))
		}

		sshConn.SetTCPAliasesAllowedUsers(allowedUsers)

		sshConn.SendMessage(fmt.Sprintf("Allowed users for TCP Aliases set to: %s", strings.Join(printKeys, ", ")), true)
	case deadlinePrefix:
		deadline, err := parseDeadline(param)
		if err != nil {
			return errors.New("invalid deadline format")
		}

		sshConn.SetDeadline(deadline)
		sshConn.SendMessage(fmt.Sprintf("Deadline for connection set to: %s", deadline.UTC().Format("2006-01-02 15:04:05")), true)
	case notePrefix:
		normalized, fromBase64 := normalizeConnectionNote(param)
		sshConn.SetConnectionNote(normalized)
		if fromBase64 {
			sshConn.SendMessage("Connection note set from base64 (auto-detected).", true)
		} else {
			sshConn.SendMessage("Connection note set.", true)
		}
	case note64Prefix:
		note, err := parseConnectionNote64(param)
		if err != nil {
			return errors.New("invalid note64 payload")
		}

		sshConn.SetConnectionNote(strings.TrimSpace(note))
		sshConn.SendMessage("Connection note set from base64.", true)
	case idPrefix:
		connectionID := strings.TrimSpace(param)
		if !validConnectionIDRegex.MatchString(connectionID) {
			return errors.New("invalid id: must be 1-50 characters and match [A-Za-z0-9._-] with no spaces")
		}

		sshConn.SetConnectionID(connectionID, true)
		sshConn.SendMessage(fmt.Sprintf("Connection id set to: %s", connectionID), true)
	}

	return nil
}

// handleAlias is used when handling a SSH connection to attach to an alias listener.
func handleAlias(newChannel ssh.NewChannel, sshConn *utils.SSHConnection, state *utils.State) {
	connection, requests, err := newChannel.Accept()
	if err != nil {
		sshConn.SetCloseInfo("server", "alias channel accept failed")
		sshConn.CleanUp(state)
		return
	}

	go ssh.DiscardRequests(requests)

	// B63: same per-connection exec-wait deadline as handleRemoteForward, so a
	// visitor opening several alias channels does not pay a fresh 1s each time
	// while the first channel still gets the full 1s exec-detection window.
	select {
	case <-sshConn.Exec:
	case <-time.After(time.Until(sshConn.ExecWaitDeadline())):
	}

	if viper.GetBool("debug") {
		log.Println("Handling alias connection for:", connection)
	}

	check := &forwardedTCPPayload{}
	err = ssh.Unmarshal(newChannel.ExtraData(), check)
	if err != nil {
		log.Println("Error unmarshaling information:", err)
		sshConn.SetCloseInfo("server", "invalid forwarded TCP payload")
		sshConn.CleanUp(state)
		return
	}

	check.Addr = strings.ToLower(check.Addr)

	tcpAliasToConnect := fmt.Sprintf("%s:%d", check.Addr, check.Port)
	if !sshConn.IsConnectionIDProvided() && strings.HasPrefix(sshConn.GetConnectionID(), "rand-") {
		sshConn.SetConnectionID(fmt.Sprintf("visitor-%s", strings.ToLower(utils.RandStringBytesMaskImprSrc(8))), false)
	}
	sshConn.MarkVisitorForwarder(fmt.Sprintf("VIS-%s", tcpAliasToConnect))
	state.IncrementVisitorAliasConnection()

	loc, ok := state.AliasListeners.Load(tcpAliasToConnect)
	if !ok {
		log.Println("Unable to load tcp alias:", tcpAliasToConnect)
		sshConn.SetCloseInfo("server", "TCP alias listener not found")
		sshConn.CleanUp(state)
		return
	}

	aH := loc

	pubKeyFingerprint := ""

	if sshConn.SSHConn.Permissions != nil {
		if _, ok := sshConn.SSHConn.Permissions.Extensions["pubKey"]; ok {
			pubKeyFingerprint = sshConn.SSHConn.Permissions.Extensions["pubKeyFingerprint"]
		}
	}

	if viper.GetBool("tcp-aliases-allowed-users") {
		connAllowed := false

		aH.SSHConnections.Range(func(name string, conn *utils.SSHConnection) bool {
			for _, fingerprint := range conn.GetTCPAliasesAllowedUsers() {
				if fingerprint == "any" || (fingerprint != "" && pubKeyFingerprint != "" && fingerprint == pubKeyFingerprint) {
					connAllowed = true
					return false
				}
			}
			return true
		})

		if !connAllowed {
			log.Println("Connection not allowed because fingerprint is not found in allowed list")
			sshConn.SetCloseInfo("server", "fingerprint not in allowed list")
			sshConn.CleanUp(state)
			return
		}
	}

	connectionLocation, err := aH.Balancer.NextServer()
	if err != nil {
		log.Println("Unable to load connection location:", err)
		sshConn.SetCloseInfo("server", "no connection location available")
		sshConn.CleanUp(state)
		return
	}

	host, err := base64.StdEncoding.DecodeString(connectionLocation.Host)
	if err != nil {
		log.Println("Unable to decode connection location:", err)
		sshConn.SetCloseInfo("server", "failed to decode connection host")
		sshConn.CleanUp(state)
		return
	}

	aliasAddr := string(host)

	connString := sshConn.SSHConn.RemoteAddr().String()
	if pubKeyFingerprint != "" {
		connString = fmt.Sprintf("%s (%s)", connString, pubKeyFingerprint)
	}

	logLine := fmt.Sprintf("Accepted connection from %s -> %s", connString, tcpAliasToConnect)
	log.Println(logLine)

	if aliasHost, aliasPort, ok := utils.ParseAliasHostPort(tcpAliasToConnect); ok {
		aH.SSHConnections.Range(func(_ string, ownerConn *utils.SSHConnection) bool {
			ownerConn.Listeners.Range(func(listenerAddr string, _ net.Listener) bool {
				if listenerAddr == aliasAddr {
					utils.WriteForwardersLogLine(utils.BuildAliasForwardersLogKey(ownerConn.GetConnectionID(), aliasHost, aliasPort), logLine)
					return false
				}

				return true
			})

			return true
		})
	}

	if viper.GetBool("log-to-client") {
		aH.SSHConnections.Range(func(key string, sshConn *utils.SSHConnection) bool {
			sshConn.Listeners.Range(func(listenerAddr string, val net.Listener) bool {
				if listenerAddr == aliasAddr {
					sshConn.SendMessage(logLine, true)

					return false
				}

				return true
			})

			return true
		})

		if sshConn.GetLocalForward() {
			sshConn.SendMessage(logLine, true)
		}
	}

	conn, err := net.Dial("unix", aliasAddr)
	if err != nil {
		log.Println("Error connecting to alias:", err)
		sshConn.SetCloseInfo("server", "failed to connect to alias socket")
		sshConn.CleanUp(state)
		return
	}

	utils.CopyBothWithBandwidthProfileGetter(conn, connection, sshConn.GetBandwidthProfileFast)
}

// writeToSession is where we write to the underlying session channel.
func writeToSession(connection ssh.Channel, c string) {
	_, err := connection.Write(append([]byte(c), []byte{'\r', '\n'}...))
	if err != nil && viper.GetBool("debug") {
		log.Println("Error trying to write message to socket:", err)
	}
}

// getProxyProtoVersion returns the proxy proto version selected by the client.
func getProxyProtoVersion(proxyProtoUserVersion string) byte {
	if viper.GetString("proxy-protocol-version") != "userdefined" {
		proxyProtoUserVersion = viper.GetString("proxy-protocol-version")
	}

	switch proxyProtoUserVersion {
	case "1":
		return 1
	case "2":
		return 2
	default:
		return 0
	}
}

// parseDeadline parses the deadline string provided by the client to a time object.
func parseDeadline(param string) (time.Time, error) {
	// Try parsing as an epoch time
	if epoch, err := strconv.ParseInt(param, 10, 64); err == nil {
		return time.Unix(epoch, 0), nil
	}

	// Try parsing as a duration
	if duration, err := time.ParseDuration(param); err == nil {
		return time.Now().Add(duration), nil
	}

	// Try parsing as a date-time
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if deadline, err := time.Parse(layout, param); err == nil {
			return deadline, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid deadline format")
}

// parseConnectionNote64 parses base64-encoded note payload.
func parseConnectionNote64(param string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(param)
	if err == nil {
		return string(decoded), nil
	}

	decoded, rawErr := base64.RawStdEncoding.DecodeString(param)
	if rawErr == nil {
		return string(decoded), nil
	}

	return "", err
}

// normalizeConnectionNote accepts plain text notes and auto-decodes base64 notes.
func normalizeConnectionNote(param string) (string, bool) {
	note := strings.TrimSpace(param)
	if note == "" {
		return "", false
	}

	if strings.ContainsAny(note, " \t\n\r") {
		return note, false
	}

	decoded, err := parseConnectionNote64(note)
	if err != nil || !utf8.ValidString(decoded) {
		return note, false
	}

	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return note, false
	}

	return decoded, true
}

// parseCommandFlags splits exec payload into flags while preserving quoted values.
func parseCommandFlags(payload string) []string {
	flags := []string{}
	var current strings.Builder

	inQuotes := false
	quoteChar := rune(0)
	escapeNext := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		flags = append(flags, current.String())
		current.Reset()
	}

	for _, r := range payload {
		if escapeNext {
			current.WriteRune(r)
			escapeNext = false
			continue
		}

		if r == '\\' {
			escapeNext = true
			continue
		}

		if inQuotes {
			if r == quoteChar {
				inQuotes = false
				quoteChar = 0
				continue
			}

			current.WriteRune(r)
			continue
		}

		if r == '\'' || r == '"' {
			inQuotes = true
			quoteChar = r
			continue
		}

		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			flush()
			continue
		}

		current.WriteRune(r)
	}

	flush()

	return flags
}

// coalesceNoteFlags merges tokens that belong to note= values when quotes are not preserved.
func coalesceNoteFlags(flags []string) []string {
	merged := []string{}

	for i := 0; i < len(flags); i++ {
		current := flags[i]

		if strings.HasPrefix(current, notePrefix+commandSplitter) {
			value := strings.TrimPrefix(current, notePrefix+commandSplitter)

			for i+1 < len(flags) {
				next := flags[i+1]
				if strings.Contains(next, commandSplitter) {
					break
				}

				value = value + " " + next
				i++
			}

			merged = append(merged, notePrefix+commandSplitter+strings.TrimSpace(value))
			continue
		}

		merged = append(merged, current)
	}

	return merged
}
