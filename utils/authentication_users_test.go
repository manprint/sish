package utils

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
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
