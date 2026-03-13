# AI Provider Abstraction
> See `/AGENTS.md` for repo rules and `internal/AGENTS.md` for boundaries.

## OVERVIEW
`internal/ai` defines the `Provider` interface for AI text completion and ships two implementations (OpenAI, Gemini) behind a config-driven factory. The package is a thin integration shim — it does not own prompts, validation, or business logic.

## WHERE TO LOOK
| Task | Location | Notes |
| --- | --- | --- |
| Provider contract | `internal/ai/provider.go` | `Message` struct and `Provider` interface |
| OpenAI backend | `internal/ai/openai.go` | `go-openai` chat completion wrapper |
| Gemini backend | `internal/ai/gemini.go` | `google.golang.org/genai` wrapper; maps system role to `SystemInstruction` |
| Factory | `internal/ai/factory.go` | `NewProvider(ctx, providerName, apiKey, model)` returns the right backend |

## CONVENTIONS
- Keep each provider implementation focused on API translation; do not embed prompt logic or business rules here.
- Default models are set per-provider (`gpt-4o`, `gemini-2.5-flash`) and overridden via the optional `AI_MODEL` config.
- The Gemini client is created once at startup (requires `context.Context` in `NewGeminiProvider`); OpenAI uses a stateless HTTP client.
- `Complete` always returns the extracted text content; callers should not need to understand provider-specific response shapes.

## ANTI-PATTERNS
- Do not add prompt construction or entitlement checks here; those belong in `internal/service/ai.go`.
- Do not log API keys or raw AI responses at INFO level; use DEBUG or redact sensitive content.
- Do not add provider-specific retry logic here; let callers (the service layer) decide retry policy.
