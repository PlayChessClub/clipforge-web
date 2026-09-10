package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const masterKeyFile = "master.key" // 与 settings.yml 同目录, 0600 权限

// masterKeyPath 返回主密钥文件绝对路径（与 configPath 同目录）。
func masterKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appDir, masterKeyFile)
}

// ensureMasterKey 返回 32 字节主密钥；不存在则用 crypto/rand 生成并 0600 落盘。
func ensureMasterKey() ([]byte, error) {
	p := masterKeyPath()
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptSecret 用 32 字节 key 对 plain 做 AES-256-GCM，返回 "<nonce_hex>:<ct_base64>"。
func encryptSecret(plain string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 字节
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	return hex.EncodeToString(nonce) + ":" + base64.StdEncoding.EncodeToString(ct), nil
}

// decryptSecret 逆操作，失败（主密钥丢失/更换）返回错误。
func decryptSecret(blob string, key []byte) (string, error) {
	i := strings.IndexByte(blob, ':')
	if i < 0 {
		return "", errors.New("bad encrypted blob")
	}
	nonce, err := hex.DecodeString(blob[:i])
	if err != nil {
		return "", err
	}
	ct, err := base64.StdEncoding.DecodeString(blob[i+1:])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", errors.New("decrypt failed: master key missing or changed")
	}
	return string(pt), nil
}
