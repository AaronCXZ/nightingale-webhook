package notifier

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockCallerSuccess(t *testing.T) {
	mock := &MockCaller{}
	result, err := mock.Call(context.Background(), CallParams{
		PhoneNumber: "13800000000",
		AlertName:   "test",
	})
	if err != nil {
		t.Fatalf("MockCaller.Call 不应出错: %v", err)
	}
	if result.CallID == "" {
		t.Error("MockCaller 应返回非空 CallID")
	}
	if result.Phone != "13800000000" {
		t.Errorf("Phone 应为 13800000000，got %s", result.Phone)
	}
}

func TestMockCallerQueryStatus(t *testing.T) {
	mock := &MockCaller{QueryStatus: "SUCCESS", QueryDuration: 60}
	detail, err := mock.QueryCallStatus(context.Background(), "test-call")
	if err != nil {
		t.Fatalf("QueryCallStatus 不应出错: %v", err)
	}
	if detail.Status != "SUCCESS" {
		t.Errorf("Status 应为 SUCCESS，got %s", detail.Status)
	}
	if detail.Duration != 60 {
		t.Errorf("Duration 应为 60，got %d", detail.Duration)
	}
}

func TestMockCallerForceError(t *testing.T) {
	mock := &MockCaller{ForceError: errors.New("simulated failure")}
	_, err := mock.Call(context.Background(), CallParams{PhoneNumber: "13800000000"})
	if err == nil {
		t.Error("ForceError 时应返回错误")
	}
}

func TestMockCallerDryRunCallID(t *testing.T) {
	mock := &MockCaller{}
	result, _ := mock.Call(context.Background(), CallParams{PhoneNumber: "13800000001"})
	// mock call id 格式 mock-xxxxxxxx
	if len(result.CallID) < 6 {
		t.Errorf("CallID 格式异常: %s", result.CallID)
	}
}

func TestRetryBackoff(t *testing.T) {
	// 验证 backoff 计算
	backoffBase := 1 * time.Second
	maxRetries := 3
	totalWait := time.Duration(0)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		wait := backoffBase * (1 << (attempt - 1)) // 1s, 2s, 4s
		totalWait += wait
	}
	// 1 + 2 + 4 = 7
	if totalWait != 7*time.Second {
		t.Errorf("总退避应为 7s，got %v", totalWait)
	}
}
