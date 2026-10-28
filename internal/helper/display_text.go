package helper

import "strings"

// IsInvisibleFormatRune 识别会改变显示顺序或隐藏内容的格式字符，供名称校验和清理共用。
// ZWJ (U+200D) 不在其中，emoji 组合序列需要它。
func IsInvisibleFormatRune(r rune) bool {
	switch {
	case r == '\u061c' || r == '\u180e' || r == '\u200b' || r == '\u200c' || r == '\u2060' || r == '\ufeff':
		return true
	case r == '\u200e' || r == '\u200f':
		return true
	case r >= '\u202a' && r <= '\u202e':
		return true
	case r >= '\u2066' && r <= '\u2069':
		return true
	default:
		return false
	}
}

// SanitizeDisplayText 清理远端上报的展示名称：去掉隐形格式字符，行／段分隔符改为空格。
// 远端名称不能像本机别名那样拒收，拒收会让插件隔离整条用量事件。
func SanitizeDisplayText(value string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case IsInvisibleFormatRune(r):
			return -1
		case r == '\u2028' || r == '\u2029':
			return ' '
		default:
			return r
		}
	}, value))
}
