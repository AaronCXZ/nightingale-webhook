package notifier

import (
	"context"
	"log/slog"
	"time"

	"webhook/internal/store"
)

const pollInterval = 15 * time.Second

// CallResultPoller 后台轮询查询呼叫结果
type CallResultPoller struct {
	s        store.Store
	caller   Caller
	interval time.Duration
}

// NewPoller 创建 poller
func NewPoller(s store.Store, caller Caller) *CallResultPoller {
	return &CallResultPoller{
		s:        s,
		caller:   caller,
		interval: pollInterval,
	}
}

// Start 启动后台轮询，通过 context 取消优雅退出
func (p *CallResultPoller) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		slog.Info("call result poller started", "interval", p.interval)

		for {
			select {
			case <-ctx.Done():
				slog.Info("call result poller stopped")
				return
			case <-ticker.C:
				p.poll(ctx)
			}
		}
	}()
}

func (p *CallResultPoller) poll(ctx context.Context) {
	records, err := p.s.GetPendingPolls()
	if err != nil {
		slog.Error("poller: GetPendingPolls failed", "error", err)
		return
	}

	if len(records) == 0 {
		return
	}

	slog.Debug("poller: checking calls", "count", len(records))

	for _, record := range records {
		p.pollOne(ctx, record)
	}
}

// pollOne 查询单条呼叫的结果并更新状态和下次回查时间
func (p *CallResultPoller) pollOne(ctx context.Context, record store.CallRecord) {
	if record.CallID == nil || *record.CallID == "" {
		return
	}

	detail, err := p.caller.QueryCallStatus(ctx, *record.CallID)
	if err != nil {
		slog.Warn("poller: QueryCallStatus failed",
			"call_id", *record.CallID,
			"error", err,
		)
		return
	}

	oldStatus := record.CallStatus

	switch detail.Status {
	case "SUCCESS":
		// 呼叫成功接通
		if err := p.s.UpdateCallResult(*record.CallID, store.CallStatusAnswered, detail.Duration); err != nil {
			slog.Error("poller: UpdateCallResult failed", "error", err)
		}
		slog.Info("call status changed",
			"call_id", *record.CallID,
			"from", oldStatus,
			"to", store.CallStatusAnswered,
			"duration", detail.Duration,
		)

	case "FAIL":
		if err := p.s.UpdateCallResult(*record.CallID, store.CallStatusFailed, 0); err != nil {
			slog.Error("poller: UpdateCallResult failed", "error", err)
		}
		slog.Info("call status changed",
			"call_id", *record.CallID,
			"from", oldStatus,
			"to", store.CallStatusFailed,
		)

	case "BUSY":
		if err := p.s.UpdateCallResult(*record.CallID, store.CallStatusBusy, 0); err != nil {
			slog.Error("poller: UpdateCallResult failed", "error", err)
		}
		slog.Info("call status changed",
			"call_id", *record.CallID,
			"from", oldStatus,
			"to", store.CallStatusBusy,
		)

	case "NO_ANSWER":
		if err := p.s.UpdateCallResult(*record.CallID, store.CallStatusNoAnswer, 0); err != nil {
			slog.Error("poller: UpdateCallResult failed", "error", err)
		}
		slog.Info("call status changed",
			"call_id", *record.CallID,
			"from", oldStatus,
			"to", store.CallStatusNoAnswer,
		)

	default:
		// 仍在进行中，更新下次回查时间
		var nextAfter time.Duration
		switch {
		case record.NextPollAt == nil:
			return
		default:
			nextAfter = 60 * time.Second
		}

		newPollAt := store.NextPollAt(nextAfter)
		if err := p.s.UpdateNextPollAt(*record.CallID, newPollAt); err != nil {
			slog.Error("poller: UpdateNextPollAt failed", "error", err)
		}
		slog.Debug("call still pending, next poll scheduled",
			"call_id", *record.CallID,
			"next", newPollAt,
		)
	}
}
