# Provider HTTP Manual Test

These checks cover Slice 1 for the shared `responses_http` path.

## OpenAI

1. Export credentials and select the OpenAI preset.
2. Run Shuttle with a one-shot prompt.
3. Verify startup logs show:
   - `preset=openai`
   - `base_url=https://api.openai.com/v1`
   - `api_key_env=OPENAI_API_KEY` or `SHUTTLE_API_KEY`
   - `model=gpt-5-nano-2025-08-07` unless overridden

```bash
export OPENAI_API_KEY=your_openai_key
go run ./cmd/shuttle \
  --socket shuttle-openai-smoke \
  --session shuttle-openai-smoke \
  --provider openai \
  --auth api_key \
  --agent "Reply with one short sentence confirming the OpenAI provider path is working."
```

Expected result: Shuttle prints a structured agent response instead of a provider auth or transport error.

## OpenRouter

1. Export credentials and select the OpenRouter preset.
2. Run Shuttle with a one-shot prompt.
3. Verify startup logs show:
   - `preset=openrouter`
   - `base_url=https://openrouter.ai/api/v1`
   - `api_key_env=OPENROUTER_API_KEY` or `SHUTTLE_API_KEY`
   - `model=openai/gpt-5` unless overridden

```bash
export OPENROUTER_API_KEY=your_openrouter_key
go run ./cmd/shuttle \
  --socket shuttle-openrouter-smoke \
  --session shuttle-openrouter-smoke \
  --provider openrouter \
  --auth api_key \
  --agent "Reply with one short sentence confirming the OpenRouter provider path is working."
```

Expected result: Shuttle prints a structured agent response instead of a provider auth or transport error.

If OpenRouter returns a model error, rerun with an explicit model override that exists on the account, for example:

```bash
go run ./cmd/shuttle \
  --socket shuttle-openrouter-smoke \
  --session shuttle-openrouter-smoke \
  --provider openrouter \
  --auth api_key \
  --model openai/gpt-5 \
  --agent "Reply with one short sentence confirming the OpenRouter provider path is working."
```
