package configtask

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	TaskTypeConfig = "config"

	opTessdata = "tessdata"
	opHugot    = "hugot"
)

type ConfigTaskHandler struct {
	logger *utils.Logger
}

func NewConfigTaskHandler(logger *utils.Logger) *ConfigTaskHandler {
	return &ConfigTaskHandler{logger: logger}
}

func (h *ConfigTaskHandler) DedupKey(payload json.RawMessage) string {
	var p struct {
		Op   string `json:"op"`
		Lang string `json:"lang"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return ""
	}
	if p.Op == opTessdata {
		return "config:tessdata:" + p.Lang
	}
	if p.Op == opHugot {
		return "config:hugot"
	}
	return ""
}

func (h *ConfigTaskHandler) Handle(ctx context.Context, t task.Task) (json.RawMessage, error) {
	var p struct {
		ConfigDir string `json:"config_dir"`
		Op        string `json:"op"`
		Lang      string `json:"lang"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, fmt.Errorf("unmarshal config task payload: %w", err)
	}

	cfg, err := config.Load(p.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("load config from %s: %w", p.ConfigDir, err)
	}

	switch p.Op {
	case opTessdata:
		if err := config.DownloadTessdataLanguage(ctx, cfg, p.Lang); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"lang": p.Lang})

	case opHugot:
		if err := config.DownloadHugotModel(ctx, cfg, h.logger); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"model": "hugot"})

	default:
		return nil, fmt.Errorf("unsupported config task operation: %q", p.Op)
	}
}
