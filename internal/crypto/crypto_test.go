package crypto

import "testing"

// TestDeriveAESKey 使用 JSDOM 实跑 3 组真实登录捕获的 (sid -> key) 对校验
// Go 版 KDF 与 JS 版 _0x46c520_ 语义完全一致。
func TestDeriveAESKey(t *testing.T) {
	cases := []struct {
		sid, magic, want string
	}{
		{"JA8Q39gBLjTSR4PvWaVjarz7Gs809If0", "ydbVj", "e7yUOzfeB:cWbktJ"},
		{"E01VVH46bY8io66xtOvh2b6n7M8JRYX5", "ydbVj", "W75u5x3c1IaUPiNZ"},
		{"vmhBF2Dg7mtIzhW80g00RLBuLJ11G914", "ydbVj", "00A/VyCdn3bVhjK:"},
		// 另一组来自更早的 verify_kdf.js 日志
		{"y99kmDRx7HvFYACo2Ng4676oauceV6Jx", "ydbVj", "Ib5fBxQc:EaUOiv7"},
	}
	for _, c := range cases {
		got, err := DeriveAESKey(c.sid, c.magic)
		if err != nil {
			t.Errorf("sid=%s: 意外错误: %v", c.sid, err)
			continue
		}
		if got != c.want {
			t.Errorf("sid=%s magic=%s: 得到 %q, 期望 %q", c.sid, c.magic, got, c.want)
		}
	}
}

// TestDeriveAESKeyInvalidInputs 验证异常 sid / magic 返回错误而非 panic。
func TestDeriveAESKeyInvalidInputs(t *testing.T) {
	if _, err := DeriveAESKey("short", "ydbVj"); err == nil {
		t.Error("sid 过短应报错")
	}
	if _, err := DeriveAESKey("JA8Q39gBLjTSR4PvWaVjarz7Gs809If0", "abc"); err == nil {
		t.Error("magic 过短应报错")
	}
}

// TestRSADecrypt 仅确认私钥能成功加载并不 panic；运行时解密行为依赖路由器响应，
// 这里只做结构性检查。
func TestRSAKeyLoad(t *testing.T) {
	k, err := loadRSAKey()
	if err != nil {
		t.Fatalf("加载 RSA 私钥失败: %v", err)
	}
	if k.N.BitLen() != 2048 {
		t.Errorf("期望 2048 位 RSA，实际 %d 位", k.N.BitLen())
	}
}

// TestAESRoundtrip 验证 AES 加解密可逆。
func TestAESRoundtrip(t *testing.T) {
	key := "e7yUOzfeB:cWbktJ"
	plain := `{"dataObj":{"username":"admin","password":"x"},"ajaxmethod":"DO_WEB_LOGIN","sessionid":"abc"}`
	cipher, err := Encrypt(plain, key)
	if err != nil {
		t.Fatalf("加密: %v", err)
	}
	got, err := Decrypt(cipher, key)
	if err != nil {
		t.Fatalf("解密: %v", err)
	}
	if got != plain {
		t.Errorf("roundtrip 不一致: %q vs %q", got, plain)
	}
}
