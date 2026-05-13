package ui

import (
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	hlStyle     = pickStyle("monokai")
	hlFormatter = formatters.Get("terminal256")

	// lexerCache memoizes lexer lookups by language token (file extension).
	// Lexers themselves are reusable. A nil value caches "no lexer for this lang"
	// so we don't repeatedly hit the registry.
	lexerCache sync.Map
)

func pickStyle(name string) *chroma.Style {
	if s := styles.Get(name); s != nil {
		return s
	}
	return styles.Fallback
}

// highlightCode runs chroma over code and returns a styled string with ANSI
// escape codes. If the lexer can't be found or any error occurs, returns the
// input unchanged so the renderer always has something to display.
func highlightCode(code, lang string) string {
	if code == "" || lang == "" || hlFormatter == nil {
		return code
	}
	lexer := lookupLexer(lang)
	if lexer == nil {
		return code
	}
	iter, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var buf strings.Builder
	if err := hlFormatter.Format(&buf, hlStyle, iter); err != nil {
		return code
	}
	return strings.TrimRight(buf.String(), "\n")
}

func lookupLexer(lang string) chroma.Lexer {
	if v, ok := lexerCache.Load(lang); ok {
		if v == nil {
			return nil
		}
		return v.(chroma.Lexer)
	}
	lx := lexers.Get(lang)
	if lx == nil {
		lexerCache.Store(lang, nil)
		return nil
	}
	lexerCache.Store(lang, lx)
	return lx
}
