package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	EncryptedPrefix      = "enc:v1:"
	DefaultMasterKeyEnv  = "DEZUXK_MASTER_KEY"
	FallbackMasterKeyEnv = "GATEWAY_MASTER_KEY"
)

var (
	ephemeralKeyOnce sync.Once
	ephemeralKey     string
)

// Vault xử lý mã hóa đối xứng AES-256-GCM cho bí mật và cookie lưu trên đĩa (Encryption at Rest)
type Vault struct {
	key []byte
}

// NewVault khởi tạo một Vault với passphrase hoặc key
func NewVault(passphraseOrKey string) *Vault {
	key := deriveKey(passphraseOrKey)
	return &Vault{key: key}
}

// Fingerprint trả về chuỗi băm rút gọn (16 ký tự hex) không thể đảo ngược,
// dùng để đối chiếu tính nhất quán giữa các node trong cụm mà không làm lộ raw secret.
func (v *Vault) Fingerprint() string {
	if len(v.key) == 0 {
		return "empty"
	}
	h := sha256.Sum256(v.key)
	return hex.EncodeToString(h[:8])
}

// ResolveMasterKey phân giải Master Key theo thứ tự ưu tiên:
// 1. Biến môi trường DEZUXK_MASTER_KEY
// 2. Biến môi trường GATEWAY_MASTER_KEY
// 3. Cấu hình security.master_key trong YAML
// 4. Khóa ngẫu nhiên sinh trong RAM cho tiến trình hiện tại (kèm cảnh báo)
func ResolveMasterKey(configKey string) string {
	if env := os.Getenv(DefaultMasterKeyEnv); env != "" {
		return env
	}
	if env := os.Getenv(FallbackMasterKeyEnv); env != "" {
		return env
	}
	if configKey != "" {
		return configKey
	}
	ephemeralKeyOnce.Do(func() {
		rnd := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, rnd); err == nil {
			ephemeralKey = hex.EncodeToString(rnd)
		} else {
			ephemeralKey = fmt.Sprintf("ephemeral-key-%d", time.Now().UnixNano())
		}
		log.Println("[Security Warning] Chưa thiết lập DEZUXK_MASTER_KEY. Hệ thống tự động sinh khóa ngẫu nhiên trong RAM cho phiên chạy này. Hãy thiết lập biến môi trường DEZUXK_MASTER_KEY để lưu cookie vĩnh viễn qua các lần restart.")
	})
	return ephemeralKey
}

func deriveKey(passphrase string) []byte {
	h := sha256.Sum256([]byte(passphrase))
	return h[:]
}

// Encrypt mã hóa dữ liệu nhị phân bằng AES-256-GCM với nonce ngẫu nhiên 12-byte
// Chuỗi trả về có định dạng: enc:v1:<base64(nonce + ciphertext + tag)>
func (v *Vault) Encrypt(plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil
	}

	block, err := aes.NewCipher(v.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return EncryptedPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt giải mã chuỗi. Nếu chuỗi KHÔNG có tiền tố enc:v1:,
// Vault coi đây là dữ liệu plaintext cũ để giữ tương thích ngược 100%.
func (v *Vault) Decrypt(cipherStr string) ([]byte, error) {
	if cipherStr == "" {
		return []byte{}, nil
	}

	if !strings.HasPrefix(cipherStr, EncryptedPrefix) {
		// Dữ liệu cũ chưa mã hóa
		return []byte(cipherStr), nil
	}

	rawB64 := strings.TrimPrefix(cipherStr, EncryptedPrefix)
	data, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(v.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("dữ liệu mã hóa không hợp lệ: độ dài quá ngắn")
	}

	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]

	return gcm.Open(nil, nonce, ciphertext, nil)
}
