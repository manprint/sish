package utils

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Generated identifiers back random subdomains, aliases, connection IDs and —
// critically — the auto-generated service console token. They used to come from
// math/rand seeded with the current nanosecond (B14).
func TestRandStringBytesMaskImprSrc(t *testing.T) {
	if got := RandStringBytesMaskImprSrc(0); got != "" {
		t.Fatalf("RandStringBytesMaskImprSrc(0)=%q, want empty", got)
	}

	if got := RandStringBytesMaskImprSrc(-5); got != "" {
		t.Fatalf("RandStringBytesMaskImprSrc(-5)=%q, want empty", got)
	}

	seen := map[string]struct{}{}
	for _, length := range []int{1, 3, 16, 20, 64} {
		for i := 0; i < 64; i++ {
			got := RandStringBytesMaskImprSrc(length)

			if len(got) != length {
				t.Fatalf("len(RandStringBytesMaskImprSrc(%d))=%d", length, len(got))
			}

			for _, c := range got {
				if !strings.ContainsRune(randStringAlphabet, c) {
					t.Fatalf("character %q outside the alphabet", c)
				}
			}

			if length >= 16 {
				if _, dup := seen[got]; dup {
					t.Fatalf("duplicate value generated: %q", got)
				}
				seen[got] = struct{}{}
			}
		}
	}
}

// A biased generator would starve part of the alphabet; rejection sampling keeps
// it uniform. This only asserts coverage, which a modulo-biased or truncated
// alphabet would break.
func TestRandStringBytesUsesWholeAlphabet(t *testing.T) {
	seen := map[rune]struct{}{}
	for i := 0; i < 400; i++ {
		for _, c := range RandStringBytesMaskImprSrc(64) {
			seen[c] = struct{}{}
		}
	}

	for _, c := range randStringAlphabet {
		if _, ok := seen[c]; !ok {
			t.Fatalf("character %q was never generated", c)
		}
	}
}

func TestSecureCompareAndCredentialsMatch(t *testing.T) {
	if !secureCompare("token", "token") {
		t.Fatal("secureCompare rejected equal values")
	}

	for _, tc := range []struct{ a, b string }{
		{"token", "tokeN"},
		{"token", "token "},
		{"token", ""},
		{"", "token"},
		{"token", "tokenlonger"},
	} {
		if secureCompare(tc.a, tc.b) {
			t.Fatalf("secureCompare(%q, %q) accepted differing values", tc.a, tc.b)
		}
	}

	if !secureCredentialsMatch("user", "pass", "user", "pass") {
		t.Fatal("secureCredentialsMatch rejected correct credentials")
	}

	if secureCredentialsMatch("user", "wrong", "user", "pass") {
		t.Fatal("secureCredentialsMatch accepted a wrong password")
	}

	if secureCredentialsMatch("wrong", "pass", "user", "pass") {
		t.Fatal("secureCredentialsMatch accepted a wrong username")
	}
}

// The internal status page rendered the raw process arguments, exposing the host
// key passphrase, console tokens and editor credentials (B17).
func TestRedactStartupFlags(t *testing.T) {
	args := []string{
		"sish",
		"--ssh-address=:2222",
		"--private-key-passphrase", "s3cr3t",
		"--service-console-token=tok3n",
		"--admin-consolle-editkeys-credentials", "admin:hunter2",
		"--authentication-password", "pw",
		"--http-address", ":80",
		"--debug",
		"positional",
	}

	want := []string{
		"sish",
		"--ssh-address=:2222",
		"--private-key-passphrase", redactedFlagValue,
		"--service-console-token=" + redactedFlagValue,
		"--admin-consolle-editkeys-credentials", redactedFlagValue,
		"--authentication-password", redactedFlagValue,
		"--http-address", ":80",
		"--debug",
		"positional",
	}

	got := redactStartupFlags(args)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redactStartupFlags()=\n%q\nwant\n%q", got, want)
	}

	// The original slice must not be mutated.
	if args[3] != "s3cr3t" {
		t.Fatalf("redactStartupFlags mutated its input: %q", args[3])
	}
}

func TestIsSecretFlagKey(t *testing.T) {
	secret := []string{
		"private-key-passphrase",
		"authentication-password",
		"service-console-token",
		"admin-consolle-editusers-credentials",
		"some-secret-thing",
		"SERVICE-CONSOLE-TOKEN",
	}
	for _, key := range secret {
		if !isSecretFlagKey(key) {
			t.Fatalf("isSecretFlagKey(%q)=false, want true", key)
		}
	}

	public := []string{"http-address", "domain", "bind-random-ports", "debug"}
	for _, key := range public {
		if isSecretFlagKey(key) {
			t.Fatalf("isSecretFlagKey(%q)=true, want false", key)
		}
	}
}

// Bind allocation checks that a target is free and registers the holder in two
// steps; without a lock around both, two forwards could claim the same
// subdomain, port or alias and leave a dirty forward behind (B20).
func TestLockBindAllocationSerializesCallers(t *testing.T) {
	var (
		mu      sync.Mutex
		active  int
		maxSeen int
		wg      sync.WaitGroup
	)

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			release := LockBindAllocation()
			defer release()

			mu.Lock()
			active++
			if active > maxSeen {
				maxSeen = active
			}
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			active--
			mu.Unlock()
		}()
	}

	wg.Wait()

	if maxSeen != 1 {
		t.Fatalf("observed %d concurrent bind allocations, want 1", maxSeen)
	}
}

// Error paths call the release function and the deferred release runs again;
// unlocking an already unlocked mutex would panic.
func TestLockBindAllocationReleaseIsIdempotent(t *testing.T) {
	release := LockBindAllocation()
	release()
	release()
	release()

	// The lock must be free afterwards.
	done := make(chan struct{})
	go func() {
		LockBindAllocation()()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bind allocation lock was left held")
	}
}

// PeekTLSHello used to return a nil error for non-TLS traffic, which callers
// logged as "Unable to read TLS hello: %!s(<nil>)" and treated as success.
func TestPeekTLSHelloRejectsNonTLS(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		_, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	}()

	hello, teeConn, err := PeekTLSHello(server)
	if teeConn != nil {
		defer teeConn.Close()
	}

	if !errors.Is(err, errNotTLSHandshake) {
		t.Fatalf("err=%v, want errNotTLSHandshake", err)
	}

	if hello != nil {
		t.Fatalf("hello=%v, want nil", hello)
	}
}
