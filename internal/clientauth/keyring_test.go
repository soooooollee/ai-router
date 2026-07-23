package clientauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeyRingGenerateVerifyAndPersist(t *testing.T) {
	t.Setenv(masterKeyEnvironment, "")
	t.Setenv(previousKeysEnv, "")
	path := filepath.Join(t.TempDir(), "credential-master.key")
	ring, err := LoadOrCreateKeyRing(path)
	if err != nil {
		t.Fatal(err)
	}
	credential, secret, err := ring.Generate("client_a", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidManagedCredentialKey(secret) || !strings.HasPrefix(secret, "sk-") || credential.Prefix != secret[:15] {
		t.Fatalf("generated key has an invalid format: secret=%q credential=%#v", secret, credential)
	}
	if !ring.Verify(credential.HMACKeyID, secret, credential.SecretHMAC) || ring.Verify(credential.HMACKeyID, secret+"x", credential.SecretHMAC) {
		t.Fatal("credential HMAC verification is incorrect")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("master key permissions are too broad: %o", info.Mode().Perm())
	}
	reloaded, err := LoadOrCreateKeyRing(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CurrentID() != ring.CurrentID() || !reloaded.Verify(credential.HMACKeyID, secret, credential.SecretHMAC) {
		t.Fatal("reloaded key ring cannot verify an existing credential")
	}
	missing := reloaded.Missing([]string{reloaded.CurrentID(), "hmac_missing"})
	if len(missing) != 1 || missing[0] != "hmac_missing" {
		t.Fatalf("key ring requirement check returned %#v", missing)
	}
}

func TestManagedCredentialCanBeRevealedOnlyWithUntamperedRecord(t *testing.T) {
	t.Setenv(masterKeyEnvironment, "")
	t.Setenv(previousKeysEnv, "")
	path := filepath.Join(t.TempDir(), "credential-master.key")
	ring, err := LoadOrCreateKeyRing(path)
	if err != nil {
		t.Fatal(err)
	}
	credential, secret, err := ring.GenerateManaged("client_managed", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !credential.View().Recoverable || len(credential.SecretCiphertext) == 0 || strings.Contains(string(credential.SecretCiphertext), secret) {
		t.Fatalf("managed credential was not encrypted: %#v", credential.View())
	}
	revealed, err := ring.Reveal(credential)
	if err != nil || revealed != secret {
		t.Fatalf("managed credential reveal failed: %q %v", revealed, err)
	}
	reloaded, err := LoadOrCreateKeyRing(path)
	if err != nil {
		t.Fatal(err)
	}
	if revealed, err = reloaded.Reveal(credential); err != nil || revealed != secret {
		t.Fatalf("reloaded key ring cannot reveal managed credential: %q %v", revealed, err)
	}
	tampered := credential
	tampered.SecretCiphertext = append([]byte(nil), credential.SecretCiphertext...)
	tampered.SecretCiphertext[0] ^= 0xff
	if _, err = ring.Reveal(tampered); err == nil {
		t.Fatal("tampered managed credential was revealed")
	}
	standard, _, err := ring.Generate("client_standard", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ring.Reveal(standard); err == nil {
		t.Fatal("standard credential was treated as recoverable")
	}
}

func TestValidManagedCredentialKeyRejectsOtherFormats(t *testing.T) {
	for _, value := range []string{"", "airoute-local", "air_sk_live_short_secret", "air_sk_prod_x_y", "sk-short", "sk-!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		if ValidManagedCredentialKey(value) {
			t.Fatalf("invalid managed credential was accepted: %q", value)
		}
	}
}

func FuzzValidManagedCredentialKey(f *testing.F) {
	for _, value := range []string{"", "sk-", "air_sk_live_", "air_sk_test_aaaaaaaaaaaaaaaaaaaa_", "Bearer secret", "?key=value"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if ValidManagedCredentialKey(value) && (!strings.HasPrefix(value, "sk-") || len(value) != 46) {
			t.Fatalf("accepted managed credential has invalid format: %q", value)
		}
	})
}
