package clientauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zbss/airoute/internal/clientstore"
	"github.com/zbss/airoute/internal/safefile"
)

const (
	masterKeyEnvironment = "AIROUTE_CREDENTIAL_MASTER_KEY"
	previousKeysEnv      = "AIROUTE_CREDENTIAL_PREVIOUS_KEYS"
)

type masterKeyFile struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Key     string `json:"key"`
}

type KeyRing struct {
	currentID string
	keys      map[string][]byte
	path      string
	external  bool
}

func LoadOrCreateKeyRing(path string) (*KeyRing, error) {
	keys := map[string][]byte{}
	var currentID string
	external := false
	if raw := strings.TrimSpace(os.Getenv(masterKeyEnvironment)); raw != "" {
		external = true
		key, err := decodeMasterKey(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", masterKeyEnvironment, err)
		}
		currentID = masterKeyID(key)
		keys[currentID] = key
	} else {
		file, err := readOrCreateMasterKey(path)
		if err != nil {
			return nil, err
		}
		key, err := decodeMasterKey(file.Key)
		if err != nil {
			return nil, fmt.Errorf("decode credential master key: %w", err)
		}
		if file.ID != masterKeyID(key) {
			return nil, errors.New("credential master key id does not match key material")
		}
		currentID = file.ID
		keys[currentID] = key
	}
	if err := loadPreviousKeys(keys); err != nil {
		return nil, err
	}
	return &KeyRing{currentID: currentID, keys: keys, path: path, external: external}, nil
}

func LoadBackupKeyRing(path string) (*KeyRing, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file masterKeyFile
	if err = json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	key, err := decodeMasterKey(file.Key)
	if err != nil || file.Version != 1 || file.ID != masterKeyID(key) {
		return nil, errors.New("backup credential master key file is invalid")
	}
	keys := map[string][]byte{file.ID: key}
	if err = loadPreviousKeys(keys); err != nil {
		return nil, err
	}
	return &KeyRing{currentID: file.ID, keys: keys, path: path}, nil
}

func loadPreviousKeys(keys map[string][]byte) error {
	if previous := strings.TrimSpace(os.Getenv(previousKeysEnv)); previous != "" {
		var encoded map[string]string
		if err := json.Unmarshal([]byte(previous), &encoded); err != nil {
			return fmt.Errorf("%s must be a JSON object of key ids to base64 keys: %w", previousKeysEnv, err)
		}
		for id, value := range encoded {
			key, err := decodeMasterKey(value)
			if err != nil || masterKeyID(key) != id {
				return fmt.Errorf("%s contains an invalid key for %s", previousKeysEnv, id)
			}
			keys[id] = key
		}
	}
	return nil
}

func readOrCreateMasterKey(path string) (masterKeyFile, error) {
	if strings.TrimSpace(path) == "" {
		return masterKeyFile{}, errors.New("credential master key path is required")
	}
	if raw, err := os.ReadFile(path); err == nil {
		var file masterKeyFile
		if err = json.Unmarshal(raw, &file); err != nil {
			return masterKeyFile{}, err
		}
		if file.Version != 1 || file.ID == "" || file.Key == "" {
			return masterKeyFile{}, errors.New("credential master key file is invalid")
		}
		if err = os.Chmod(path, 0600); err != nil {
			return masterKeyFile{}, err
		}
		return file, nil
	} else if !os.IsNotExist(err) {
		return masterKeyFile{}, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return masterKeyFile{}, err
	}
	file := masterKeyFile{Version: 1, ID: masterKeyID(key), Key: base64.RawURLEncoding.EncodeToString(key)}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return masterKeyFile{}, err
	}
	if err = safefile.AtomicWrite(path, append(raw, '\n'), 0600); err != nil {
		return masterKeyFile{}, err
	}
	return file, nil
}

func decodeMasterKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(key) < 32 {
		return nil, errors.New("master key must be at least 32 bytes encoded as base64url")
	}
	return key, nil
}

func masterKeyID(key []byte) string {
	digest := sha256.Sum256(key)
	return "hmac_" + hex.EncodeToString(digest[:8])
}

func (k *KeyRing) CurrentID() string { return k.currentID }

func (k *KeyRing) IDs() []string {
	ids := make([]string, 0, len(k.keys))
	for id := range k.keys {
		ids = append(ids, id)
	}
	return ids
}

func (k *KeyRing) Missing(ids []string) []string {
	missing := make([]string, 0)
	for _, id := range ids {
		if len(k.keys[id]) == 0 {
			missing = append(missing, id)
		}
	}
	return missing
}

func (k *KeyRing) Path() string { return k.path }

func (k *KeyRing) BackupPath() string {
	if k.external {
		return ""
	}
	return k.path
}

func (k *KeyRing) Digest(key string) ([]byte, string, error) {
	material := k.keys[k.currentID]
	if len(material) == 0 {
		return nil, "", errors.New("current credential master key is unavailable")
	}
	digest := hmac.New(sha256.New, material)
	_, _ = digest.Write([]byte(key))
	return digest.Sum(nil), k.currentID, nil
}

func (k *KeyRing) Digests(key string) map[string][]byte {
	out := make(map[string][]byte, len(k.keys))
	for id, material := range k.keys {
		digest := hmac.New(sha256.New, material)
		_, _ = digest.Write([]byte(key))
		out[id] = digest.Sum(nil)
	}
	return out
}

func (k *KeyRing) Verify(keyID, key string, expected []byte) bool {
	material := k.keys[keyID]
	if len(material) == 0 || len(expected) == 0 {
		return false
	}
	digest := hmac.New(sha256.New, material)
	_, _ = digest.Write([]byte(key))
	actual := digest.Sum(nil)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare(actual, expected) == 1
}

func (k *KeyRing) Generate(clientID string, expiresAt *time.Time, _ bool) (clientstore.Credential, string, error) {
	return k.generate(clientID, expiresAt, clientstore.CredentialStandard)
}

func (k *KeyRing) GenerateManaged(clientID string, expiresAt *time.Time, _ bool) (clientstore.Credential, string, error) {
	return k.generate(clientID, expiresAt, clientstore.CredentialManaged)
}

func (k *KeyRing) generate(clientID string, expiresAt *time.Time, kind clientstore.CredentialKind) (clientstore.Credential, string, error) {
	idBytes := make([]byte, 12)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return clientstore.Credential{}, "", err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return clientstore.Credential{}, "", err
	}
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(idBytes))
	complete := "sk-" + base64.RawURLEncoding.EncodeToString(secretBytes)
	prefix := complete[:15]
	digest, keyID, err := k.Digest(complete)
	if err != nil {
		return clientstore.Credential{}, "", err
	}
	credential := clientstore.Credential{
		ID: id, ClientID: clientID, Kind: kind, Prefix: prefix,
		SecretHMAC: digest, HMACKeyID: keyID, Status: clientstore.CredentialActive,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt,
	}
	if kind == clientstore.CredentialManaged {
		if err = k.seal(&credential, complete); err != nil {
			return clientstore.Credential{}, "", err
		}
	}
	return credential, complete, nil
}

func credentialAAD(credential clientstore.Credential) []byte {
	return []byte(credential.ID + "\x00" + credential.ClientID + "\x00" + credential.Prefix)
}

func encryptionKey(master []byte) []byte {
	digest := hmac.New(sha256.New, master)
	_, _ = digest.Write([]byte("airoute/managed-credential/aes-gcm/v1"))
	return digest.Sum(nil)
}

func managedCipher(master []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(encryptionKey(master))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (k *KeyRing) seal(credential *clientstore.Credential, complete string) error {
	master := k.keys[credential.HMACKeyID]
	if len(master) == 0 {
		return errors.New("credential encryption master key is unavailable")
	}
	aead, err := managedCipher(master)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	credential.SecretNonce = nonce
	credential.SecretCiphertext = aead.Seal(nil, nonce, []byte(complete), credentialAAD(*credential))
	return nil
}

func (k *KeyRing) Reveal(credential clientstore.Credential) (string, error) {
	if credential.Kind != clientstore.CredentialManaged || len(credential.SecretCiphertext) == 0 || len(credential.SecretNonce) == 0 {
		return "", errors.New("credential is not recoverable")
	}
	master := k.keys[credential.HMACKeyID]
	if len(master) == 0 {
		return "", errors.New("credential encryption master key is unavailable")
	}
	aead, err := managedCipher(master)
	if err != nil {
		return "", err
	}
	if len(credential.SecretNonce) != aead.NonceSize() {
		return "", errors.New("managed credential nonce is invalid")
	}
	plain, err := aead.Open(nil, credential.SecretNonce, credential.SecretCiphertext, credentialAAD(credential))
	if err != nil {
		return "", errors.New("managed credential could not be decrypted")
	}
	complete := string(plain)
	if !k.Verify(credential.HMACKeyID, complete, credential.SecretHMAC) {
		return "", errors.New("managed credential digest does not match encrypted value")
	}
	return complete, nil
}

func ValidManagedCredentialKey(value string) bool {
	if !strings.HasPrefix(value, "sk-") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "sk-"))
	return err == nil && len(decoded) == 32
}
