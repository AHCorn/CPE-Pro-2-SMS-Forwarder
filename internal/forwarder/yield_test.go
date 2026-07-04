package forwarder

import (
	"testing"
	"time"
)

// TestDecideLoginAction 覆盖自动退避决策的各分支：
// 无人占用 / 自身占用直接登录；他人占用进入并维持退避；退避超时转强制登录；
// 占用者 IP 未知（空）时不应误判为自己。
func TestDecideLoginAction(t *testing.T) {
	const force = 10 * time.Minute
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	self := "192.168.2.10"
	other := "192.168.2.200"
	zero := time.Time{}

	tests := []struct {
		name          string
		occupied      bool
		holderIP      string
		localIP       string
		yieldingSince time.Time
		now           time.Time
		wantAction    loginAction
		wantYielding  time.Time // 期望返回的 yieldingSince
	}{
		{
			name:       "无人占用直接登录",
			occupied:   false,
			holderIP:   "",
			localIP:    self,
			now:        now,
			wantAction: actionLogin,
			// 不在退避，yieldingSince 应清零
			wantYielding: zero,
		},
		{
			name:       "占用者是自身陈旧会话则直接登录",
			occupied:   true,
			holderIP:   self,
			localIP:    self,
			now:        now,
			wantAction: actionLogin,
			// 自身占用（如定期重登/瞬时心跳失败）不退避
			wantYielding: zero,
		},
		{
			name:     "他人占用且首次发现则进入退避",
			occupied: true,
			holderIP: other,
			localIP:  self,
			// 此前未退避
			yieldingSince: zero,
			now:           now,
			wantAction:    actionYield,
			// 以本次为退避起点
			wantYielding: now,
		},
		{
			name:          "他人占用且退避未到上限则维持退避",
			occupied:      true,
			holderIP:      other,
			localIP:       self,
			yieldingSince: now.Add(-5 * time.Minute),
			now:           now,
			wantAction:    actionYield,
			// 起点保持不变
			wantYielding: now.Add(-5 * time.Minute),
		},
		{
			name:          "他人占用且退避恰好到上限则强制登录",
			occupied:      true,
			holderIP:      other,
			localIP:       self,
			yieldingSince: now.Add(-force),
			now:           now,
			wantAction:    actionForceLogin,
			wantYielding:  zero,
		},
		{
			name:          "他人占用且退避超过上限则强制登录",
			occupied:      true,
			holderIP:      other,
			localIP:       self,
			yieldingSince: now.Add(-force - time.Minute),
			now:           now,
			wantAction:    actionForceLogin,
			wantYielding:  zero,
		},
		{
			name:         "占用者IP未知时不误判为自身仍退避",
			occupied:     true,
			holderIP:     "",
			localIP:      self,
			now:          now,
			wantAction:   actionYield,
			wantYielding: now,
		},
		{
			name:         "本地IP未知时不误判为自身仍退避",
			occupied:     true,
			holderIP:     self,
			localIP:      "",
			now:          now,
			wantAction:   actionYield,
			wantYielding: now,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAction, gotYielding := decideLoginAction(
				tt.occupied, tt.holderIP, tt.localIP, tt.yieldingSince, tt.now, force,
			)
			if gotAction != tt.wantAction {
				t.Errorf("action = %v, 期望 %v", gotAction, tt.wantAction)
			}
			if !gotYielding.Equal(tt.wantYielding) {
				t.Errorf("yieldingSince = %v, 期望 %v", gotYielding, tt.wantYielding)
			}
		})
	}
}
