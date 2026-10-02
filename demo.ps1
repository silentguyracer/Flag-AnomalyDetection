# Interactive PowerShell Demo for Fraud & Anomaly Detection Pipeline (Tier-1 Defense Edition)

param(
    [string]$Target = "fraud-demo"
)

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "   Fraud & Anomaly Flagging Service - Ultimate Suite      " -ForegroundColor Cyan
Write-Host "   Pre-Auth Gate, Graph Cycles, Consortium, XAI, SAR, RL  " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

switch ($Target) {
    "test" {
        Write-Host "`n>>> Running Full Test Suite across All Packages..." -ForegroundColor Yellow
        go test -count=1 -v ./...
    }
    "build" {
        Write-Host "`n>>> Building all binaries..." -ForegroundColor Yellow
        go build -o bin/fraudctl.exe ./cmd/fraudctl
        go build -o bin/fraud-service.exe ./cmd/fraud-service
        go build -o bin/outbox-relay.exe ./cmd/outbox-relay
        go build -o bin/load-gen.exe ./cmd/load-gen
        Write-Host "Build complete in ./bin/" -ForegroundColor Green
    }
    "train-ml" {
        Write-Host "`n>>> Training Calibrated ML Fraud Classifier..." -ForegroundColor Yellow
        python ml/train.py
    }
    "fraud-demo" {
        Write-Host "`n[STEP 1/13] Running Core Scenario Integration Tests (12/12)..." -ForegroundColor Yellow
        go test -v ./test/...

        Write-Host "`n[STEP 2/13] Synchronous Pre-Auth Gate (<25ms SLA)..." -ForegroundColor Yellow
        Write-Host "--- Test A: Low-Risk Everyday Spend (Expect APPROVE) ---" -ForegroundColor Cyan
        .\bin\fraudctl.exe auth-check --amount 1500 --country GB --channel contactless

        Write-Host "--- Test B: Severe Risk Anomaly (Expect DECLINE) ---" -ForegroundColor Red
        .\bin\fraudctl.exe auth-check --amount 45000 --country JP --channel online --merchant "Tokyo Luxury"

        Write-Host "`n[STEP 3/13] Regulatory Explainable AI (XAI) & Counterfactual Notice..." -ForegroundColor Yellow
        .\bin\fraudctl.exe explain --amount 45000 --country JP --channel online --merchant "Tokyo Luxury Direct" --mcc 6051

        Write-Host "`n[STEP 4/13] Entity Graph: Circular Laundering Loop (Cycle Detection)..." -ForegroundColor Yellow
        .\bin\fraudctl.exe cycle-detect --depth 5

        Write-Host "`n[STEP 5/13] Entity Graph: Personalized PageRank Dirty Money Diffusion..." -ForegroundColor Yellow
        .\bin\fraudctl.exe risk-diffusion --min-risk 0.15

        Write-Host "`n[STEP 6/13] Cryptographic Zero-PII Consortium Threat Query..." -ForegroundColor Yellow
        .\bin\fraudctl.exe consortium-query --token card_pan_compromised_darkweb_9918

        Write-Host "`n[STEP 7/13] Behavioral Biometrics & Neuromuscular Cadence..." -ForegroundColor Yellow
        Write-Host "--- Profile A: Synthetic Automation Bot ---" -ForegroundColor Red
        .\bin\fraudctl.exe biometrics-check -synthetic=true
        Write-Host "--- Profile B: Human Shopper ---" -ForegroundColor Green
        .\bin\fraudctl.exe biometrics-check -synthetic=false

        Write-Host "`n[STEP 8/13] Contextual Multi-Armed Bandit (Thompson Sampling Thresholds)..." -ForegroundColor Yellow
        .\bin\fraudctl.exe bandit-tune --episodes 500

        Write-Host "`n[STEP 9/13] Automated Regulatory Suspicious Activity Report (FinCEN Part V)..." -ForegroundColor Yellow
        .\bin\fraudctl.exe sar --amount 1250000

        Write-Host "`n[STEP 10/13] Live Differential Shadow Canary & Promotion Safety..." -ForegroundColor Yellow
        .\bin\fraudctl.exe canary-status --samples 5000

        Write-Host "`n[STEP 11/13] Population Stability Index (PSI) Concept Drift Check..." -ForegroundColor Yellow
        .\bin\fraudctl.exe drift-check --samples 1000

        Write-Host "`n[STEP 12/13] Rules Engine Benchmark (Candidate v3 vs Ground Truth)..." -ForegroundColor Yellow
        .\bin\fraudctl.exe backtest --rules configs/rules.yaml --samples 10000

        Write-Host "`n[STEP 13/13] Live Attack Scenario Simulations..." -ForegroundColor Yellow
        Write-Host "`n--- Card Testing Attack Simulation ---" -ForegroundColor Magenta
        .\bin\fraudctl.exe simulate --scenario card_testing

        Write-Host "`n--- Account Takeover Simulation ---" -ForegroundColor Magenta
        .\bin\fraudctl.exe simulate --scenario account_takeover

        Write-Host "`n==========================================================" -ForegroundColor Green
        Write-Host "   Tier-1 Anti-Financial Crime Suite Demo Completed!       " -ForegroundColor Green
        Write-Host "==========================================================" -ForegroundColor Green
    }
    default {
        Write-Host "Unknown target: $Target. Available: fraud-demo, test, build, train-ml" -ForegroundColor Red
    }
}
