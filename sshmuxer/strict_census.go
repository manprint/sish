package sshmuxer

import (
	"log"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/spf13/viper"
)

// startStrictIDCensedConnectionEnforcer disconnects active forwarded clients when
// their explicit connection ID is no longer present in the refreshed census cache.
func startStrictIDCensedConnectionEnforcer(state *utils.State) {
	if !viper.GetBool("census-enabled") {
		return
	}

	go func() {
		lastSeenRefresh := time.Time{}
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if !utils.IsStrictIDCensedEnabled() {
				continue
			}

			snapshot := utils.GetCensusCacheSnapshot()
			if snapshot.LastRefresh.IsZero() || !snapshot.LastRefresh.After(lastSeenRefresh) {
				continue
			}

			lastSeenRefresh = snapshot.LastRefresh

			// Collect offenders during the Range, then act afterwards. Doing
			// the notify+cleanup inline would block the single enforcer
			// goroutine on a connection whose session reader is gone, freezing
			// enforcement for every other connection (B3).
			var toClose []*utils.SSHConnection
			state.SSHConnections.Range(func(_ string, sshConn *utils.SSHConnection) bool {
				if sshConn == nil {
					return true
				}

				if !sshConn.IsConnectionIDProvided() {
					return true
				}

				// Only enforce for active forwarded connections.
				if sshConn.ListenerCount() <= 0 {
					return true
				}

				if utils.IsIDCensed(sshConn.GetConnectionID()) {
					return true
				}

				toClose = append(toClose, sshConn)
				return true
			})

			for _, sshConn := range toClose {
				// Best-effort, non-blocking notify: a connection with no active
				// session reader must not wedge the enforcer.
				sshConn.SendMessage("Forwarded id is not censed.", false)
				sshConn.SetCloseInfo("server", "ID no longer censed")
				sshConn.CleanUp(state)
			}

			if len(toClose) > 0 {
				log.Printf("Strict census enforcement closed %d connection(s) with non-censed IDs", len(toClose))
			}
		}
	}()
}
