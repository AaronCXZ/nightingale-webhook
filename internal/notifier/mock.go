package notifier

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// MockCaller 模拟呼叫器（用于 dry run 和测试）
type MockCaller struct {
	// ForceError 强制 Call 返回错误（测试重试逻辑）
	ForceError error
	// ForceQueryError 强制 QueryCallStatus 返回错误
	ForceQueryError error
	// QueryStatus 模拟回查返回的状态
	QueryStatus string
	// QueryDuration 模拟回查返回的通话时长
	QueryDuration int64
}

// Call 模拟呼叫
func (m *MockCaller) Call(ctx context.Context, params CallParams) (CallResult, error) {
	if m.ForceError != nil {
		return CallResult{}, m.ForceError
	}
	callID := fmt.Sprintf("mock-%s", uuid.New().String()[:8])
	return CallResult{CallID: callID, Phone: params.PhoneNumber}, nil
}

// QueryCallStatus 模拟查询呼叫结果
func (m *MockCaller) QueryCallStatus(ctx context.Context, callID string) (*CallDetail, error) {
	if m.ForceQueryError != nil {
		return nil, m.ForceQueryError
	}
	status := m.QueryStatus
	if status == "" {
		status = "SUCCESS"
	}
	return &CallDetail{
		CallID:   callID,
		Status:   status,
		Duration: m.QueryDuration,
	}, nil
}
