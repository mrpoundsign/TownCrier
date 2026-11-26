package bot

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// TemplateContext provides variable & argument lookup for single-pass %token% interpolation.
type TemplateContext struct {
	Args      map[string]string
	Store     *Store
	Platform  string
	UserID    string
	LookupOBS func(key string) (string, bool)
}

// Interpolate expands %key%, %global.key%, %user.key%, %random(min,max)%, and %pick(a,b,c)%
// in a single pass so substituted values containing '%' cannot trigger recursive expansion.
func Interpolate(tmpl string, ctx TemplateContext) string {
	if tmpl == "" || !strings.Contains(tmpl, "%") {
		return tmpl
	}

	var b strings.Builder
	b.Grow(len(tmpl) + 16)

	i := 0
	for i < len(tmpl) {
		if tmpl[i] != '%' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		endRel := strings.IndexByte(tmpl[i+1:], '%')
		if endRel < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		token := tmpl[i+1 : i+1+endRel]
		if replacement, ok := evalToken(token, ctx); ok {
			b.WriteString(replacement)
			i = i + 1 + endRel + 1
			continue
		}
		// Not a recognized token (e.g. "50% of %something"): keep '%' literal and continue scanning.
		b.WriteByte('%')
		i++
	}

	return b.String()
}

func evalToken(token string, ctx TemplateContext) (string, bool) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "", false
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "random(") && strings.HasSuffix(trimmed, ")") {
		inner := trimmed[len("random(") : len(trimmed)-1]
		parts := strings.Split(inner, ",")
		if len(parts) == 2 {
			minVal, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			maxVal, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				if maxVal < minVal {
					minVal, maxVal = maxVal, minVal
				}
				if minVal == maxVal {
					return strconv.Itoa(minVal), true
				}
				return strconv.Itoa(minVal + rand.IntN(maxVal-minVal+1)), true
			}
		}
		return "", false
	}

	if strings.HasPrefix(lower, "pick(") && strings.HasSuffix(trimmed, ")") {
		inner := trimmed[len("pick(") : len(trimmed)-1]
		parts := strings.Split(inner, ",")
		if len(parts) > 0 {
			choice := strings.TrimSpace(parts[rand.IntN(len(parts))])
			return choice, true
		}
		return "", false
	}

	if strings.HasPrefix(lower, "global.") {
		key := strings.TrimSpace(trimmed[len("global."):])
		if key != "" && ctx.Store != nil {
			if e, ok := ctx.Store.GetGlobalVar(key, false); ok {
				return e.Value, true
			}
			if e, ok := ctx.Store.GetGlobalVar(key, true); ok {
				return e.Value, true
			}
			return "", true
		}
	}

	if strings.HasPrefix(lower, "user.") {
		key := strings.TrimSpace(trimmed[len("user."):])
		if key != "" && ctx.Store != nil {
			if e, ok := ctx.Store.GetUserVar(ctx.Platform, ctx.UserID, key); ok {
				return e.Value, true
			}
			return "", true
		}
	}

	if ctx.LookupOBS != nil {
		var obsKey string
		switch lower {
		case "obs.scene", "obsscene":
			obsKey = "scene"
		case "obs.previousscene", "obspreviousscene":
			obsKey = "previous_scene"
		case "obs.previewscene", "obspreviewscene":
			obsKey = "preview_scene"
		case "obs.connected", "obsconnected":
			obsKey = "connected"
		}
		if obsKey != "" {
			if val, ok := ctx.LookupOBS(obsKey); ok {
				return val, true
			}
		}
	}

	if ctx.Args != nil {
		if val, ok := ctx.Args[trimmed]; ok {
			return val, true
		}
		for k, v := range ctx.Args {
			if strings.EqualFold(k, trimmed) {
				return v, true
			}
		}
	}
	return "", false
}
