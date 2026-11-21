// Package all registers every provider's driver; import it for its side effect.
package all

import (
	_ "github.com/squadrn/squadrn-cli/internal/provider/claude"
	_ "github.com/squadrn/squadrn-cli/internal/provider/codex"
	_ "github.com/squadrn/squadrn-cli/internal/provider/gemini"
)
