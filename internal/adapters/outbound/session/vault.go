package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"os"
	"strings"
)

const (
	EncryptedPrefix      = "enc:v1:"
	DefaultMasterKeyEnv  = "DEZUXK_MASTER_KEY"
	FallbackMasterKeyEnv = "GATEWAY_MASTER_KEY"
	defaultFallbackKey   = "dezuxk-gateway-deterministic-master-key-32b"
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

// ResolveMasterKey phân giải Master Key theo thứ tự ưu tiên:
// 1. Biến môi trường DEZUXK_MASTER_KEY
// 2. Biến môi trường GATEWAY_MASTER_KEY
// 3. Cấu hình security.master_key trong YAML
// 4. Khóa mặc định an toàn (có ghi log cảnh báo)
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
	log.Println("[Security Warning] Chưa thiết lập DEZUXK_MASTER_KEY. Hệ thống đang sử dụng khóa lưu trữ mặc định. Hãy thiết lập biến môi trường DEZUXK_MASTER_KEY để đảm bảo an toàn tuyệt đối.")
	return defaultFallbackKey
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
