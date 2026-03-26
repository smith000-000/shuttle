#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

echo "==> Native patch engine tests"
go test ./internal/patchapply -v

echo "==> Controller and TUI patch flow tests"
go test ./internal/controller -run 'TestLocalController(ApplyProposedPatch|ApprovePatchRunsPatchPath|ContinueAfterPatchApply|ContinueAfterFailedPatchApply)' -v
go test ./internal/tui -run 'TestPatchProposal(ActionCardOmitsEditAffordance|AutoContinueUsesPatchContinuation|FailureStillAutoContinuesThroughPatchContinuation)' -v

echo "==> Interactive patch harness tests"
go test ./integration/harness -run 'TestInteractiveHarness(AppliesPatchProposal|RetriesFailedPatchProposal)' -v
