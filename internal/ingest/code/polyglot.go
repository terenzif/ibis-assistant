package code

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	reASPXServerScript = regexp.MustCompile(`(?is)<script([^>]*)runat\s*=\s*["']?server["']?([^>]*)>(.*?)</script>`)
	// RE2 has no negative lookahead; skip directive/expression blocks in the loop.
	reASPXCodeBlock = regexp.MustCompile(`(?s)<%(.*?)%>`)
	rePolyglotExt   = regexp.MustCompile(`(?i)\.(aspx|ascx|cshtml|razor)$`)
	reHTMLLikeExt   = regexp.MustCompile(`(?i)\.(aspx|ascx|cshtml|razor|vue|html|htm)$`)
)

// IsPolyglotPath reports whether the path is a known mixed-language web file.
func IsPolyglotPath(path string) bool {
	return reHTMLLikeExt.MatchString(path)
}

// IsASPXFamily reports ASP.NET markup that often embeds C# server code.
func IsASPXFamily(path string) bool {
	return rePolyglotExt.MatchString(path)
}

type polyglotRegion struct {
	Lang      string // csharp, javascript, …
	Body      string
	StartLine uint32 // 1-based line in the host file
}

// extractASPXServerRegions pulls runat=server scripts and <% … %> code blocks.
// Bodies are wrapped in a synthetic class so csharp-chunk (method/class kinds) can match.
func extractASPXServerRegions(content []byte) []polyglotRegion {
	src := string(content)
	var out []polyglotRegion

	for _, m := range reASPXServerScript.FindAllStringSubmatchIndex(src, -1) {
		if len(m) < 8 {
			continue
		}
		body := strings.TrimSpace(src[m[6]:m[7]])
		if body == "" {
			continue
		}
		out = append(out, polyglotRegion{
			Lang:      "csharp",
			Body:      wrapCSharpSnippet(body),
			StartLine: lineOfOffset(src, m[6]),
		})
	}
	for _, m := range reASPXCodeBlock.FindAllStringSubmatchIndex(src, -1) {
		if len(m) < 4 {
			continue
		}
		body := strings.TrimSpace(src[m[2]:m[3]])
		if body == "" || len(body) < 8 {
			continue
		}
		// Skip <%@ ... %> directives and <%= ... %> expressions.
		if body[0] == '@' || body[0] == '=' {
			continue
		}
		out = append(out, polyglotRegion{
			Lang:      "csharp",
			Body:      wrapCSharpSnippet(body),
			StartLine: lineOfOffset(src, m[2]),
		})
	}
	return out
}

func wrapCSharpSnippet(body string) string {
	body = strings.TrimSpace(body)
	// Already looks like a type declaration — use as-is.
	if strings.Contains(body, "class ") || strings.Contains(body, "interface ") || strings.Contains(body, "struct ") {
		return body + "\n"
	}
	return "class __IbisASPX {\n" + body + "\n}\n"
}

func lineOfOffset(src string, offset int) uint32 {
	if offset <= 0 {
		return 1
	}
	if offset > len(src) {
		offset = len(src)
	}
	n := 1
	for i := 0; i < offset; {
		r, size := utf8.DecodeRuneInString(src[i:])
		if r == '\n' {
			n++
		}
		i += size
	}
	return uint32(n)
}

func regionExt(lang string) string {
	switch strings.ToLower(lang) {
	case "csharp", "c#":
		return ".cs"
	case "javascript", "js":
		return ".js"
	case "typescript", "ts":
		return ".ts"
	case "css":
		return ".css"
	default:
		return ".txt"
	}
}
