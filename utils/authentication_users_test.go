package utils

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

type testConnMetadata struct {
	user string
}

func (t testConnMetadata) User() string {
	return t.user
}

func (t testConnMetadata) SessionID() []byte {
	return []byte("session")
}

func (t testConnMetadata) ClientVersion() []byte {
	return []byte("SSH-2.0-client")
}

func (t testConnMetadata) ServerVersion() []byte {
	return []byte("SSH-2.0-server")
}

func (t testConnMetadata) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}

func (t testConnMetadata) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
}

func TestLoadAuthUsersReload(t *testing.T) {
	viper.Reset()
	authUsersHolderLock.Lock()
	authUsersHolder = map[string]string{}
	authUsersPublicKeysHolder = map[string][]ssh.PublicKey{}
	authUsersHolderLock.Unlock()

	dir, err := os.MkdirTemp("", "sish_auth_users")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}()

	fileA := filepath.Join(dir, "users-a.yml")
	fileB := filepath.Join(dir, "users-b.yaml")

	err = os.WriteFile(fileA, []byte("users:\n  - name: alpha\n    password: \"A-pass\"\n  - name: beta\n    password: \"B-pass\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(fileB, []byte("users:\n  - name: gamma\n    password: \"G-pass\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	viper.Set("auth-users-enabled", true)
	viper.Set("auth-users-directory", dir)

	loadAuthUsers()

	if !checkAuthUserPassword("alpha", []byte("A-pass")) {
		t.Fatal("expected alpha to authenticate")
	}
	if !checkAuthUserPassword("beta", []byte("B-pass")) {
		t.Fatal("expected beta to authenticate")
	}
	if !checkAuthUserPassword("gamma", []byte("G-pass")) {
		t.Fatal("expected gamma to authenticate")
	}

	err = os.Remove(fileB)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(fileA, []byte("users:\n  - name: alpha\n    password: \"A-pass\"\n  - name: beta\n    password: \"B-pass-updated\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	loadAuthUsers()

	if !checkAuthUserPassword("beta", []byte("B-pass-updated")) {
		t.Fatal("expected beta password update to be applied")
	}
	if checkAuthUserPassword("beta", []byte("B-pass")) {
		t.Fatal("expected old beta password to stop working")
	}
	if checkAuthUserPassword("gamma", []byte("G-pass")) {
		t.Fatal("expected gamma to be removed after file deletion")
	}
}

func TestPasswordCallbackSupportsAuthUsersWithoutRegression(t *testing.T) {
	viper.Reset()
	authUsersHolderLock.Lock()
	authUsersHolder = map[string]string{}
	authUsersPublicKeysHolder = map[string][]ssh.PublicKey{}
	authUsersHolderLock.Unlock()

	dir, err := os.MkdirTemp("", "sish_auth_users")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}()

	privateKeyDir := filepath.Join(dir, "hostkeys")
	if err := os.MkdirAll(privateKeyDir, 0755); err != nil {
		t.Fatal(err)
	}

	usersFile := filepath.Join(dir, "users.yml")
	err = os.WriteFile(usersFile, []byte("users:\n  - name: alpha\n    password: \"A-pass\"\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}

	viper.Set("authentication", true)
	viper.Set("private-keys-directory", privateKeyDir)
	viper.Set("auth-users-enabled", true)
	viper.Set("auth-users-directory", dir)
	viper.Set("authentication-password", "global-pass")
	viper.Set("authentication-password-request-url", "")

	loadAuthUsers()
	cfg := GetSSHConfig()

	if cfg.PasswordCallback == nil {
		t.Fatal("expected password callback to be configured")
	}

	metaAlpha := testConnMetadata{user: "alpha"}
	metaOther := testConnMetadata{user: "other"}

	if _, err := cfg.PasswordCallback(metaAlpha, []byte("A-pass")); err != nil {
		t.Fatalf("expected auth-users password to authenticate, got error: %v", err)
	}

	if _, err := cfg.PasswordCallback(metaOther, []byte("global-pass")); err != nil {
		t.Fatalf("expected authentication-password to remain valid, got error: %v", err)
	}

	if _, err := cfg.PasswordCallback(metaAlpha, []byte("wrong")); err == nil {
		t.Fatal("expected wrong password to be rejected")
	}

	viper.Set("authentication-password", "")
	cfg = GetSSHConfig()
	if cfg.PasswordCallback == nil {
		t.Fatal("expected password callback when auth-users-enabled=true and auth-users are configured")
	}
}

func TestPublicKeyCallbackSupportsAuthUsersPubKey(t *testing.T) {
	viper.Reset()
	authUsersHolderLock.Lock()
	authUsersHolder = map[string]string{}
	authUsersPublicKeysHolder = map[string][]ssh.PublicKey{}
	authUsersHolderLock.Unlock()

	dir, err := os.MkdirTemp("", "sish_auth_users_pubkey")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	}()

	privateKeyDir := filepath.Join(dir, "hostkeys")
	if err := os.MkdirAll(privateKeyDir, 0755); err != nil {
		t.Fatal(err)
	}

	rawPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	userPubKey, err := ssh.NewPublicKey(rawPub)
	if err != nil {
		t.Fatal(err)
	}

	usersYAML := "users:\n" +
		"  - name: pippo\n" +
		"    password: \"synclab2023\"\n" +
		"    pubkey: \"" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(userPubKey))) + "\"\n" +
		"  - name: pluto\n" +
		"    pubkey: \"" + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(userPubKey))) + "\"\n"

	usersFile := filepath.Join(dir, "users.yml")
	err = os.WriteFile(usersFile, []byte(usersYAML), 0600)
	if err != nil {
		t.Fatal(err)
	}

	viper.Set("authentication", true)
	viper.Set("private-keys-directory", privateKeyDir)
	viper.Set("auth-users-enabled", true)
	viper.Set("auth-users-directory", dir)
	viper.Set("authentication-password", "")
	viper.Set("authentication-key-request-url", "")
	viper.Set("authentication-password-request-url", "")

	loadAuthUsers()
	cfg := GetSSHConfig()

	if cfg.PublicKeyCallback == nil {
		t.Fatal("expected public key callback to be configured")
	}

	if _, err := cfg.PublicKeyCallback(testConnMetadata{user: "pippo"}, userPubKey); err != nil {
		t.Fatalf("expected pippo key auth to succeed, got error: %v", err)
	}

	if _, err := cfg.PublicKeyCallback(testConnMetadata{user: "pluto"}, userPubKey); err != nil {
		t.Fatalf("expected pluto pubkey-only auth to succeed, got error: %v", err)
	}

	otherRawPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPubKey, err := ssh.NewPublicKey(otherRawPub)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cfg.PublicKeyCallback(testConnMetadata{user: "pluto"}, otherPubKey); err == nil {
		t.Fatal("expected unknown public key to be rejected")
	}
}

func TestParseAuthUserBandwidthConfig(t *testing.T) {
	cases := []struct {
		name        string
		user        authUser
		expectError bool
		expectLimit bool
	}{
		{
			name: "no limits provided",
			user: authUser{Name: "paperino"},
		},
		{
			name:        "upload only",
			user:        authUser{Name: "guest", BandwidthUpload: "10"},
			expectLimit: true,
		},
		{
			name:        "upload and download with burst",
			user:        authUser{Name: "pippo", BandwidthUpload: "10", BandwidthDownload: "20", BandwidthBurst: "1.5"},
			expectLimit: true,
		},
		{
			name:        "invalid upload value",
			user:        authUser{Name: "pluto", BandwidthUpload: "abc"},
			expectError: true,
		},
		{
			name:        "invalid burst value",
			user:        authUser{Name: "pluto", BandwidthDownload: "5", BandwidthBurst: "0"},
			expectError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, hasLimit, err := parseAuthUserBandwidthConfig(tc.user)
			if tc.expectError && err == nil {
				t.Fatal("expected error but got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if hasLimit != tc.expectLimit {
				t.Fatalf("unexpected hasLimit value: got %t want %t", hasLimit, tc.expectLimit)
			}
			if hasLimit && cfg.Burst <= 0 {
				t.Fatal("expected positive burst for limited profile")
			}
		})
	}
}

func TestBuildAuthUserPermissionsBandwidthFlag(t *testing.T) {
	viper.Reset()
	authUsersHolderLock.Lock()
	authUsersBandwidthHolder = map[string]authUserBandwidthConfig{
		"pippo": {
			UploadBps:   1250000,
			DownloadBps: 2500000,
			Burst:       1.5,
		},
	}
	authUsersHolderLock.Unlock()

	permsDisabled := buildAuthUserPermissions("pippo", nil, nil)
	if permsDisabled != nil {
		t.Fatal("expected nil permissions when limiter flag is disabled")
	}

	viper.Set("user-bandwidth-limiter-enabled", true)
	permsEnabled := buildAuthUserPermissions("pippo", nil, nil)
	if permsEnabled == nil || permsEnabled.Extensions == nil {
		t.Fatal("expected permissions extensions when limiter flag is enabled")
	}

	if permsEnabled.Extensions[authUserBandwidthUploadExtKey] == "" {
		t.Fatal("expected upload extension")
	}
	if permsEnabled.Extensions[authUserBandwidthDownloadExtKey] == "" {
		t.Fatal("expected download extension")
	}
	if permsEnabled.Extensions[authUserBandwidthBurstExtKey] == "" {
		t.Fatal("expected burst extension")
	}
}
