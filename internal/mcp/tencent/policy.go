package tencent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Policy struct {
	AllowedRegions   map[string]struct{}
	AllowedResources map[string]struct{}
}

func exactSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func commaSet(raw string) map[string]struct{} {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return exactSet(strings.Split(raw, ",")...)
}

func (p Policy) authorize(region, resourceID string, unfilteredList bool) error {
	if len(p.AllowedRegions) > 0 && region != "" {
		if _, ok := p.AllowedRegions[region]; !ok {
			return fmt.Errorf("region %q is outside CLOUD_SKILLS_ALLOWED_REGIONS", region)
		}
	}
	if len(p.AllowedResources) == 0 {
		return nil
	}
	if unfilteredList || resourceID == "" {
		return fmt.Errorf("unfiltered list is disabled by CLOUD_SKILLS_ALLOWED_RESOURCES")
	}
	if _, ok := p.AllowedResources[resourceID]; !ok {
		return fmt.Errorf("resource %q is outside CLOUD_SKILLS_ALLOWED_RESOURCES", resourceID)
	}
	return nil
}

type AuditEvent struct {
	Time       time.Time `json:"time"`
	Tool       string    `json:"tool"`
	Service    string    `json:"service"`
	Action     string    `json:"action"`
	ResourceID string    `json:"resource_id,omitempty"`
	Region     string    `json:"region,omitempty"`
	Mutation   bool      `json:"mutation"`
	Outcome    string    `json:"outcome"`
	RequestID  string    `json:"request_id,omitempty"`
}

type AuditSink func(context.Context, AuditEvent) error

func FileAuditSink(path string) AuditSink {
	return func(ctx context.Context, event AuditEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("audit path is empty")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create audit directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open audit log: %w", err)
		}
		defer file.Close()
		if err := file.Chmod(0o600); err != nil {
			return fmt.Errorf("secure audit log: %w", err)
		}
		line, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode audit event: %w", err)
		}
		writer := bufio.NewWriter(file)
		if _, err := writer.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("write audit event: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush audit event: %w", err)
		}
		return file.Sync()
	}
}

func sortedSetValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
