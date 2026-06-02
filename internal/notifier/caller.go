package notifier

import "context"

// CallParams 呼叫参数
type CallParams struct {
	PhoneNumber string
	AlertName   string
	Severity    int
	Value       string
	GroupName   string
}

// CallResult 呼叫结果
type CallResult struct {
	CallID string `json:"call_id"`
	Phone  string `json:"phone"`
	Error  error  `json:"-"`
}

// CallDetail 呼叫详情（回查结果）
type CallDetail struct {
	CallID   string `json:"call_id"`
	Status   string `json:"status"`   // SUCCESS/FAIL/BUSY/NO_ANSWER
	Duration int64  `json:"duration"` // 通话时长（秒）
	RingTime int64  `json:"ring_time"`
}

// Caller 呼叫接口
type Caller interface {
	// Call 发起语音呼叫（含重试逻辑）
	Call(ctx context.Context, params CallParams) (CallResult, error)
	// QueryCallStatus 查询呼叫结果
	QueryCallStatus(ctx context.Context, callID string) (*CallDetail, error)
}
