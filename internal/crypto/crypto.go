// Package crypto 实现烽火 5G CPE 路由器登录所需的加解密原语：RSA 解密 is_encrypt
// 握手值（magic）、由 sessionid + magic 派生 AES-128 密钥、AES-128-CBC 加解密。
// 算法与路由器前端 polyfill.js 保持一致；完整 HTTP 登录流程见 internal/cpe。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
)

// rsaPrivKeyPKCS8Base64 是烽火路由器前端 polyfill.js 里硬编码的 RSA-2048 私钥
// （PKCS#8 DER base64）。
const rsaPrivKeyPKCS8Base64 = "MIIEuwIBADANBgkqhkiG9w0BAQEFAASCBKUwggShAgEAAoIBAQC3Ij9dkS9pmNeORIuDOFavCpO92Ieh/jglB8vAqxe3pA2VakZNNHN7caHvNb4DTCAMlRcFrRVf1K2CGHKdrgiyJGe0kwAnCwUTihCe9jkMzl+ZNKHYtWufUv7ISh+FZXfHi/huyl/MqIPzagIB3glpAJ4T7T7gWGT1+fklekZ8URfMqbTXUa/3QBCmwfKrdv4YiIOuPqpifxYK9BWeNYKUOyWbF+dKVi9Wns9mU55Tvw23yF1/jYpXmzv9MTZnybgnIKnXdXmBSyGxxxj9hVqdvQuMyslForGxxHIIDaGS26vZTdqfJGeFoBCHHMXzGaNC/1uF0x87VA7au4jxuBORAgMBAAECgf8CgPVc0h9T0kMgLs+5e4uz2PEsJ0mzbUZXO0QN3kj0ucl1wX40kAMELQmJu7JdWS0W/vLRoQwpwz6cCLmIbliwFs9UKK5X2k63davEgJlHE4s7DP0peVF/XCMfmePUbw60K7W5zgqBQcyMB2b/n4mBZgDDRPsXFh5LPp+pY4KTMIK2TcWCTOi+7SeoNkKaOXwfkprBCYSNdi6OociMVLUaVd16MHofseuaUKPXoTI8vaa7MbT4RGXEPiuXbwfN6G5j9Gi3zN8c+1Im9rLLcXUNnwxJCmt5cIsSdKY/BwL5YbfzwWZSSfqnVIZmYZFfCFLy45EfNWf8tJNODy5QlQkCgYEA38XpPi2TPVkCRfOL4pjeWRW4C6fa0ziBgtJtN4Elx7o401BgUNNuOW4/NhTMSlf1Z9qEGi2M2Eyj1xT8ZMlH2+fILAwV+G2sQIY4NY9XMHsPGGCsDOIvYvw76yYXnBOcaOX1+jY+g2ING1Zb+P0ymR8lXb/a3Y+gKhfeEYqbA+UCgYEA0YIKzX/pAYWyxJhgr+8ki5DkYCCZGb3H2v0APqb9CNbgFtkEEdgJTdcpRKuEgkmIp+iR7i9TiwCy3rwHUn5wodn5cSnGq/nIRRa+YtGJc4qmjEo0TZJNcmx4U4vjKfTDc6wZkaQ9nzGF5Fww3xMh5u+0Rh+IfH74dktMN3hxLj0CgYAwpU6SNMgocvwahtpnFUJo7V7IMeJRPpxw+xvBEDNNWv9VeMinaX8xvvTA5f6PPtXbkNZc9oAC2Y5YiHhh1Jvpg1axtKLmEbl7gXIgupuCr43Vh9Z/KoCQrTK9aNeDF4RODYfOsBIg76TXx4tQ8oIYZXvzCG0k8z8nR28AMziFvQKBgQC3YkG8cQruZy38gXiYZxYxCAmuzrnUW1cVq0FMlfSEiTkrJpg2WkiClyQrVIqvVFhGyP77YveYg2sOJb2vCrfiJB8AW9Xn8MLJHshVTR4oQaPYxpcTk00xLBsC3j5gGjv/AxR6dC3wK3QMWFn62Q9iykyc2LsqZiVrvisfntBK7QKBgAJu8Z3zUHojUtOTsiziwiApOdNfYhHpkp8fUYsskYaxb8DIdthYnZIV1hfh+0Qqly7zv8ykys3q+g0HFiBpd6L9VhE1BTgnvDf1dsag1nk6+/JFWYaOJVD0IGtDT0pRtiEPM9EYp6H2gMnN8/CRt6ndUJwqeV9xjw6Sl0ftaKuF"

// FixedIV 是登录及所有加密接口共用的 AES-128-CBC IV。
// 字面值对应 polyfill.js 中 CryptoJS.enc.Utf8.parse 出的固定 16 字节。
var FixedIV = []byte("opqrstuvwxyz{|}~")

var (
	rsaKeyOnce sync.Once
	rsaKey     *rsa.PrivateKey
	rsaKeyErr  error
)

// 懒加载并缓存解析后的 PKCS#8 私钥。
func loadRSAKey() (*rsa.PrivateKey, error) {
	rsaKeyOnce.Do(func() {
		der, err := base64.StdEncoding.DecodeString(rsaPrivKeyPKCS8Base64)
		if err != nil {
			rsaKeyErr = fmt.Errorf("RSA 私钥 base64 解码: %w", err)
			return
		}
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			rsaKeyErr = fmt.Errorf("RSA 私钥 PKCS8 解析: %w", err)
			return
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			rsaKeyErr = fmt.Errorf("RSA 私钥类型断言失败")
			return
		}
		rsaKey = rk
	})
	return rsaKey, rsaKeyErr
}

// RSADecryptIsEncrypt 解密 /fh_api/tmp/FHNCAPIS?ajaxmethod=is_encrypt 响应里的
// data 字段（base64 编码）。服务端先在密文前面拼了 6 个随机 ASCII 字符用于
// 混淆，真正的 RSA 密文从第 7 个字符开始（对应 JS 里的 data.substr(6)）。
//
// 返回解密后的 plaintext 字符串（本固件上长度为 5），作为 KDF 的 magic 输入。
func RSADecryptIsEncrypt(dataB64 string) (string, error) {
	if len(dataB64) <= 6 {
		return "", fmt.Errorf("is_encrypt 响应过短: %d", len(dataB64))
	}
	key, err := loadRSAKey()
	if err != nil {
		return "", err
	}
	// 先 base64 解码整条，再跳过前 6 字节。注意 substr(6) 是在 base64 文本上
	// 做的，所以必须先切 base64 字符串，再解码
	ct, err := base64.StdEncoding.DecodeString(dataB64[6:])
	if err != nil {
		return "", fmt.Errorf("密文 base64 解码: %w", err)
	}
	//lint:ignore SA1019 固件协议固定使用 PKCS1v15，且本端仅作为客户端解密固件下发的单个值，不构成解密预言机
	plain, err := rsa.DecryptPKCS1v15(nil, key, ct)
	if err != nil {
		return "", fmt.Errorf("RSA PKCS1v15 解密: %w", err)
	}
	return string(plain), nil
}

// specialKeyPositions 是 KDF 中从 magic 取字符的固定 5 个下标；
// 因此 magic 长度必须 >= 5（当前固件为 5）。
var specialKeyPositions = []int{5, 7, 10, 11, 13}

// DeriveAESKey 还原自 polyfill.js 中 _0x46c520_ 函数，根据 sessionid 与 magic
// 生成 16 字节 AES-128 密钥。下列样本可用于校验 (sid, magic -> key)：
//
//	sid="JA8Q39gBLjTSR4PvWaVjarz7Gs809If0", magic="ydbVj" -> "e7yUOzfeB:cWbktJ"
//	sid="E01VVH46bY8io66xtOvh2b6n7M8JRYX5", magic="ydbVj" -> "W75u5x3c1IaUPiNZ"
//
// sid/magic 长度异常时返回错误而非 panic，便于上游重登录或告警。
func DeriveAESKey(sid, magic string) (string, error) {
	// sid 当前固件恒为 32 字符；至少需要 30 以保证主分支/兜底分支下标合法
	if len(sid) < 30 {
		return "", fmt.Errorf("sessionid 长度异常: %d < 30", len(sid))
	}
	if len(magic) < len(specialKeyPositions) {
		return "", fmt.Errorf("magic 长度异常: %d < %d", len(magic), len(specialKeyPositions))
	}
	specialSet := make(map[int]bool, len(specialKeyPositions))
	for _, p := range specialKeyPositions {
		specialSet[p] = true
	}
	// offset 依赖 sid[1] 的字符码；公式 (charCode(sid[1]) % 3) - 1 取值 ∈ {-1,0,1}
	offset := int(sid[1])%3 - 1

	out := make([]byte, 16)
	mc := 0 // magic counter
	for i := 0; i < 16; i++ {
		var c int
		switch {
		case specialSet[i]:
			c = int(magic[mc]) + offset
			mc++
		case i*4+2 < len(sid):
			// 主分支：从 sid 末尾往前按 4 步取，减 1
			c = int(sid[len(sid)-2-i*4]) - 1
		default:
			// 兜底分支：从 sid 前部按 4i-31 的偏移取，加 1
			idx := i*4 - 31
			if idx < 0 || idx >= len(sid) {
				return "", fmt.Errorf("KDF 兜底分支下标越界 i=%d idx=%d len(sid)=%d", i, idx, len(sid))
			}
			c = int(sid[idx]) + 1
		}
		out[i] = byte(c) // #nosec G115 -- c 由协议字符码 ±offset 推导，落在 byte 范围；此截断即 KDF 算法本身，已被 crypto_test 向量校验
	}
	return string(out), nil
}

// RandomPrefix 生成 6 字符 base36 小写随机串，用于拼在加密 body 前面；
// 对应 JS: Math.random().toString(36).substr(2,6)。
func RandomPrefix() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 6)
	if _, err := crand.Read(b); err != nil {
		// 退化兜底：该 6 字符前缀只是与浏览器行为对齐的协议混淆位，固件会剥离、
		// 不参与机密性或完整性；取不到强随机时退回定值不影响安全。
		for i := range b {
			b[i] = alphabet[i%len(alphabet)]
		}
		return string(b)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	padding := make([]byte, pad)
	for i := range padding {
		padding[i] = byte(pad) // #nosec G115 -- pad 恒为 1..16(blockSize)，不溢出
	}
	return append(data, padding...)
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(data) {
		return data
	}
	return data[:len(data)-padLen]
}

// Encrypt 用指定 AES-128 key 加密明文，返回 hex 密文（不含随机前缀）。
func Encrypt(plaintext, key string) (string, error) {
	if len(key) != 16 {
		return "", fmt.Errorf("AES key 长度必须为 16: 实际 %d", len(key))
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad([]byte(plaintext), aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, FixedIV).CryptBlocks(ct, padded) // #nosec G407 -- IV 由固件协议固定，必须与之匹配，无法改用随机 IV
	return hex.EncodeToString(ct), nil
}

// Decrypt 用指定 AES-128 key 解密 hex 密文（不含随机前缀）。
func Decrypt(hexCipher, key string) (string, error) {
	if len(key) != 16 {
		return "", fmt.Errorf("AES key 长度必须为 16: 实际 %d", len(key))
	}
	ct, err := hex.DecodeString(hexCipher)
	if err != nil {
		return "", fmt.Errorf("hex 解码: %w", err)
	}
	if len(ct)%aes.BlockSize != 0 {
		return "", fmt.Errorf("密文长度 %d 不是 %d 的倍数", len(ct), aes.BlockSize)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, FixedIV).CryptBlocks(pt, ct)
	return string(pkcs7Unpad(pt)), nil
}
