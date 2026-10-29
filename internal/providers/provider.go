// Package providers 定义 LLM 额度/余额查询的通用接口与注册表。
//
// 不同 provider 的"余额"语义各不相同：百炼 Token Plan 是滚动窗口的已用百分比，
// OpenAI 是 USD 限额，DeepSeek 是 CNY 余额，OpenRouter 是 credits。
// 统一用 Section 切片归一化，展示层无需感知具体 provider。
package providers

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Section 描述一种额度维度，例如百炼的 5 小时窗口 / 周窗口。
type Section struct {
	Name      string    `json:"name"`
	UsedPct   float64   `json:"used_pct"`          // 已用百分比(0-100)；-1 表示无此语义(如无限额)
	Remaining float64   `json:"remaining"`         // 绝对剩余量(如 USD/credits)；-1 表示无此语义
	ResetAt   time.Time `json:"reset_at,omitzero"` // 窗口重置时间；零值表示不适用
}

// Balance 是某个 provider 在一次轮询中的完整额度快照。
type Balance struct {
	Provider  string    `json:"provider"`
	Sections  []Section `json:"sections"`
	FetchedAt time.Time `json:"fetched_at"`
	Err       string    `json:"error,omitempty"`
}

// Provider 是余额查询适配器的统一接口。
// 新增 provider = 实现该接口 + 在 init() 中 Register。
type Provider interface {
	Name() string
	Query(ctx context.Context) (Balance, error)
}

type Scheduled interface {
	PollInterval() time.Duration
}

// KeyRequired 由必须填写 API Key 才能查询的 provider 实现。
type KeyRequired interface {
	RequiresAPIKey() bool
}

// AccessKeyReporter 由支持 AccessKey 的 provider 实现；maskedID 不含 secret。
type AccessKeyReporter interface {
	AccessKeyStatus() (source string, maskedID string)
}

// Options 由 config 层填充后传给各 provider 的工厂函数。
type Options struct {
	APIKey          string        // 可选；需要 API Key 的 provider 使用
	AccessKeyID     string        // 可选；阿里云 AK ID
	AccessKeySecret string        // 可选；阿里云 AK Secret
	Timeout         time.Duration // 单次查询超时；<=0 使用 provider 默认值
	DataDir         string        // daemon 缓存目录；读本地快照的 provider 使用
}

// Factory 构造一个 Provider。
type Factory func(Options) (Provider, error)

var registry = map[string]Factory{}

// Register 在 init() 中调用，把 provider 注册进全局注册表。
func Register(name string, f Factory) { registry[name] = f }

// New 按名称构造 provider。
func New(name string, opts Options) (Provider, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("未知的 provider %q（已注册: %v）", name, Names())
	}
	return f(opts)
}

// Names 返回已注册的 provider 名称（有序）。
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
