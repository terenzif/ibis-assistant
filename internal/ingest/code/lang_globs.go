package code

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var langGlobMu sync.Mutex

// EnsureLanguageGlob adds ext (e.g. ".aspx") under languageGlobs[language] in sgconfig.yml
// if missing. Extension-agnostic: used for any unfamiliar host mapping the AI or bootstrap suggests.
func EnsureLanguageGlob(ext, language string) error {
	ext = strings.ToLower(strings.TrimSpace(ext))
	language = strings.ToLower(strings.TrimSpace(language))
	if ext == "" || language == "" {
		return nil
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	glob := `"*` + ext + `"`

	langGlobMu.Lock()
	defer langGlobMu.Unlock()

	root := findSGConfigDir()
	if root == "" {
		return fmt.Errorf("sgconfig root not found")
	}
	cfgPath := filepath.Join(root, "sgconfig.yml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	text := string(raw)
	needle := "*" + ext
	if strings.Contains(text, needle) {
		return nil // already mapped (any language)
	}

	updated := upsertLanguageGlob(text, language, glob)
	if updated == text {
		return nil
	}
	if err := os.WriteFile(cfgPath, []byte(updated), 0644); err != nil {
		return err
	}
	// Mirror to testrun when present
	testrunCfg := filepath.Join(root, "testrun", "sgconfig.yml")
	if _, err := os.Stat(filepath.Dir(testrunCfg)); err == nil {
		_ = os.WriteFile(testrunCfg, []byte(updated), 0644)
	}
	_ = EnsureAstGrepRules()
	return nil
}

func upsertLanguageGlob(text, language, quotedGlob string) string {
	// Ensure languageGlobs section exists
	if !strings.Contains(text, "languageGlobs:") {
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "languageGlobs:\n  " + language + ":\n    - " + quotedGlob + "\n"
		return text
	}
	lines := strings.Split(text, "\n")
	var out []string
	inGlobs := false
	inLang := false
	langIndent := ""
	inserted := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if trim == "languageGlobs:" {
			inGlobs = true
			inLang = false
			out = append(out, line)
			continue
		}
		if inGlobs {
			if trim != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				// left the block
				if !inserted && inLang {
					out = append(out, langIndent+"- "+quotedGlob)
					inserted = true
				}
				inGlobs = false
				inLang = false
				out = append(out, line)
				continue
			}
			if strings.HasPrefix(trim, language+":") {
				inLang = true
				langIndent = indentation(line) + "  "
				out = append(out, line)
				continue
			}
			if inLang && strings.HasPrefix(trim, "- ") {
				out = append(out, line)
				continue
			}
			if inLang && trim != "" && strings.Contains(trim, ":") && !strings.HasPrefix(trim, "-") {
				// next language key
				if !inserted {
					out = append(out, langIndent+"- "+quotedGlob)
					inserted = true
				}
				inLang = false
				out = append(out, line)
				if strings.HasSuffix(trim, ":") {
					// might be another lang under globs
					key := strings.TrimSuffix(trim, ":")
					if key != "" {
						inLang = key == language
						if inLang {
							langIndent = indentation(line) + "  "
						}
					}
				}
				continue
			}
		}
		out = append(out, line)
	}
	if inGlobs && inLang && !inserted {
		out = append(out, langIndent+"- "+quotedGlob)
		inserted = true
	}
	if inGlobs && !inserted {
		// language key missing entirely
		out = append(out, "  "+language+":", "    - "+quotedGlob)
		inserted = true
	}
	_ = inserted
	return strings.Join(out, "\n")
}

func indentation(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}
