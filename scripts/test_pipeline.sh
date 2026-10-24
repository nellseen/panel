#!/usr/bin/env bash
set -e

echo "================================================================="
echo "        TERMUX NODEPANEL — COMPREHENSIVE TEST PIPELINE           "
echo "================================================================="

echo ""
echo ">>> STAGE 1: Static Checks & Compilation Validation"
echo "[*] Checking Go formatting (gofmt)..."
test -z "$(gofmt -l internal cmd tests)" || (echo "gofmt found unformatted files" && exit 1)
echo "    [PASS] gofmt clean"

echo "[*] Running go vet..."
go vet ./...
echo "    [PASS] go vet passed"

echo "[*] Running TypeScript type check (tsc --noEmit)..."
npm run lint
echo "    [PASS] TypeScript passed"

echo "[*] Building React static assets with Vite..."
npm run build
echo "    [PASS] Frontend Vite build passed"

echo "[*] Compiling standalone Go binary (bin/nodepanel)..."
mkdir -p bin
go build -o bin/nodepanel ./cmd/panel
echo "    [PASS] Go binary compiled successfully"

echo ""
echo ">>> STAGE 2: Core Unit Tests"
go test -v ./internal/runtime ./internal/packages ./internal/storage ./internal/repository
echo "    [PASS] All core unit tests passed"

echo ""
echo ">>> STAGE 3: Process Supervisor & Child Process Tests"
go test -v ./internal/process ./internal/server ./internal/api
echo "    [PASS] Process supervisor & API tests passed"

echo ""
echo ">>> STAGE 4: Concurrency Isolation & E2E Integration Tests"
go test -v ./tests/...
echo "    [PASS] E2E concurrency isolation & load tests passed"

echo ""
echo ">>> STAGE 5: Real Runtime CLI Diagnostics"
./bin/nodepanel version
./bin/nodepanel doctor
./bin/nodepanel runtime > /dev/null
echo "    [PASS] nodepanel CLI doctor & runtime commands executed successfully"

echo ""
echo "================================================================="
echo "       ALL TEST STAGES COMPLETED SUCCESSFULLY WITH 0 FAILURES    "
echo "================================================================="
