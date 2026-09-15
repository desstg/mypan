package logx

import (
	"context"
	"io"
	"log/slog"
	"time"
)

type multiHandler struct {
	stdout slog.Handler
	file   slog.Handler
}

func newMultiHandler(stdout slog.Handler, storage *Storage, level *slog.LevelVar) *multiHandler {
	return &multiHandler{
		stdout: stdout,
		file:   &fileHandler{storage: storage, level: level},
	}
}

func (h *multiHandler) Enabled(_ context.Context, level slog.Level) bool {
	return h.stdout.Enabled(context.Background(), level)
}

func (h *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.stdout.Enabled(ctx, r.Level) {
		_ = h.stdout.Handle(ctx, r)
	}
	return h.file.Handle(ctx, r)
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &multiHandler{
		stdout: h.stdout.WithAttrs(attrs),
		file:   h.file.WithAttrs(attrs),
	}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	return &multiHandler{
		stdout: h.stdout.WithGroup(name),
		file:   h.file.WithGroup(name),
	}
}

type fileHandler struct {
	storage *Storage
	level   *slog.LevelVar
	attrs   []slog.Attr
	groups  []string
}

func (h *fileHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *fileHandler) Handle(_ context.Context, r slog.Record) error {
	if !h.Enabled(context.Background(), r.Level) {
		return nil
	}
	e := recordToEntry(r, h.attrs, h.groups)
	h.storage.Enqueue(e)
	return nil
}

func (h *fileHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	dup := *h
	dup.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &dup
}

func (h *fileHandler) WithGroup(name string) slog.Handler {
	dup := *h
	dup.groups = append(append([]string{}, h.groups...), name)
	return &dup
}

func recordToEntry(r slog.Record, baseAttrs []slog.Attr, groups []string) Entry {
	attrs := append([]slog.Attr{}, baseAttrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})

	e := Entry{
		Timestamp: r.Time.Format(time.RFC3339),
		Level:     LevelToInt(r.Level),
		Module:    string(ModuleSystem),
		Message:   r.Message,
		Details:   map[string]any{},
	}

	for _, a := range attrs {
		key := a.Key
		if len(groups) > 0 {
			key = groups[0] + "." + key
		}
		switch key {
		case "module":
			e.Module = attrString(a)
		case "account_id":
			e.AccountID = attrAny(a)
		case "driver_name", "driver":
			e.DriverName = attrAny(a)
		case "account", "account_name":
			if e.Details == nil {
				e.Details = map[string]any{}
			}
			e.Details[key] = attrAny(a)
		default:
			e.Details[key] = attrAny(a)
		}
	}
	if len(e.Details) == 0 {
		e.Details = nil
	}
	return e
}

func attrString(a slog.Attr) string {
	return a.Value.String()
}

func attrAny(a slog.Attr) any {
	switch a.Value.Kind() {
	case slog.KindString:
		return a.Value.String()
	case slog.KindInt64:
		return a.Value.Int64()
	case slog.KindUint64:
		return a.Value.Uint64()
	case slog.KindFloat64:
		return a.Value.Float64()
	case slog.KindBool:
		return a.Value.Bool()
	case slog.KindDuration:
		return a.Value.Duration().String()
	case slog.KindTime:
		return a.Value.Time().Format(time.RFC3339)
	default:
		return a.Value.Any()
	}
}

func newStdoutHandler(w io.Writer, lv *slog.LevelVar) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String("time", a.Value.Time().Format("15:04:05"))
			}
			return a
		},
	})
}
