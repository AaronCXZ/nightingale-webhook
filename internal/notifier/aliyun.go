package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	dyvmsapi "github.com/alibabacloud-go/dyvmsapi-20170525/v6/client"
)

// aliyunCaller 阿里云语音呼叫实现
type aliyunCaller struct {
	client      *dyvmsapi.Client
	showNumber  string
	ttsCode     string
	playTimes   int
	maxRetries  int
	backoffBase time.Duration
}

// NewAliyunCaller 创建阿里云呼叫器（惰性初始化，不进行网络调用）
func NewAliyunCaller(accessKeyID, accessKeySecret, showNumber, ttsCode string, playTimes, maxRetries int, backoffBase time.Duration) (Caller, error) {
	endpoint := "dyvmsapi.aliyuncs.com"
	config := &openapiutil.Config{
		AccessKeyId:     &accessKeyID,
		AccessKeySecret: &accessKeySecret,
		Endpoint:        &endpoint,
	}

	client, err := dyvmsapi.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("创建阿里云 client 失败: %w", err)
	}

	return &aliyunCaller{
		client:      client,
		showNumber:  showNumber,
		ttsCode:     ttsCode,
		playTimes:   playTimes,
		maxRetries:  maxRetries,
		backoffBase: backoffBase,
	}, nil
}

// Call 发起呼叫（含指数退避重试）
func (a *aliyunCaller) Call(ctx context.Context, params CallParams) (CallResult, error) {
	var lastErr error

	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		if attempt > 0 {
			wait := a.backoffBase * (1 << (attempt - 1)) // 1s, 2s, 4s...
			slog.Warn("call failed, retrying",
				"attempt", attempt,
				"phone", params.PhoneNumber,
				"wait", wait,
				"error", lastErr,
			)

			select {
			case <-ctx.Done():
				return CallResult{Phone: params.PhoneNumber, Error: ctx.Err()}, ctx.Err()
			case <-time.After(wait):
			}
		}

		result, err := a.singleCall(ctx, params)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}

	return CallResult{
		Phone: params.PhoneNumber,
		Error: fmt.Errorf("重试 %d 次后仍失败: %w", a.maxRetries, lastErr),
	}, lastErr
}

// singleCall 单次呼叫
func (a *aliyunCaller) singleCall(ctx context.Context, params CallParams) (CallResult, error) {
	ttsMap := map[string]string{
		"severity": severityLabel(params.Severity),
		"alert":    params.AlertName,
		"value":    params.Value,
		"group":    params.GroupName,
	}
	ttsBytes, _ := json.Marshal(ttsMap)
	ttsParamStr := string(ttsBytes)
	playTimes := int32(a.playTimes)

	request := &dyvmsapi.SingleCallByTtsRequest{
		CalledShowNumber: &a.showNumber,
		CalledNumber:     &params.PhoneNumber,
		TtsCode:          &a.ttsCode,
		TtsParam:         &ttsParamStr,
		PlayTimes:        &playTimes,
	}

	response, err := a.client.SingleCallByTts(request)
	if err != nil {
		return CallResult{Phone: params.PhoneNumber}, fmt.Errorf("阿里云 SingleCallByTts 失败: %w", err)
	}

	if response.Body == nil || response.Body.CallId == nil {
		return CallResult{Phone: params.PhoneNumber}, fmt.Errorf("阿里云返回的 CallId 为空")
	}

	slog.Info("call initiated",
		"phone", params.PhoneNumber,
		"alert", params.AlertName,
		"call_id", *response.Body.CallId,
	)

	return CallResult{CallID: *response.Body.CallId, Phone: params.PhoneNumber}, nil
}

// QueryCallStatus 查询呼叫结果
// 返回的 Data 是 JSON 字符串，需要解析
func (a *aliyunCaller) QueryCallStatus(ctx context.Context, callID string) (*CallDetail, error) {
	prodID := int64(11000000300006) // 语音通知产品 ID
	queryDate := time.Now().UnixMilli()

	request := &dyvmsapi.QueryCallDetailByCallIdRequest{
		CallId:    &callID,
		ProdId:    &prodID,
		QueryDate: &queryDate,
	}

	response, err := a.client.QueryCallDetailByCallId(request)
	if err != nil {
		return nil, fmt.Errorf("查询呼叫详情失败: %w", err)
	}

	if response.Body == nil || response.Body.Data == nil {
		return nil, fmt.Errorf("查询呼叫详情返回为空")
	}

	// Data 是 JSON 字符串，解析为 callDetailData
	var data callDetailData
	if err := json.Unmarshal([]byte(*response.Body.Data), &data); err != nil {
		return nil, fmt.Errorf("解析呼叫详情 JSON 失败: %w", err)
	}

	detail := &CallDetail{
		CallID:   callID,
		Status:   data.StateDesc,
		Duration: data.Duration,
	}

	return detail, nil
}

// callDetailData 阿里云 QueryCallDetailByCallId 返回的 Data JSON 结构
type callDetailData struct {
	Caller    string `json:"caller"`
	Called    string `json:"called"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Duration  int64  `json:"duration"`
	StateDesc string `json:"stateDesc"` // SUCCESS/FAIL/BUSY/NO_ANSWER
	StateCode string `json:"stateCode"`
}

func severityLabel(level int) string {
	switch level {
	case 1:
		return "严重"
	case 2:
		return "警告"
	case 3:
		return "提醒"
	default:
		return "未知"
	}
}
