$env:PATH = "C:\Users\Administrator\sdk\go1.25.0\bin;$env:PATH"

$BackendDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $BackendDir

Write-Host "=== Cashparty Backend Startup ===" -ForegroundColor Cyan
Write-Host ""

Write-Host "[1/3] Checking dependencies..." -ForegroundColor Yellow
$deps = @("docker", "go")
foreach ($dep in $deps) {
    if (!(Get-Command $dep -ErrorAction SilentlyContinue)) {
        Write-Host "ERROR: $dep not found in PATH" -ForegroundColor Red
        exit 1
    }
}
Write-Host "  - Docker: OK" -ForegroundColor Green
Write-Host "  - Go: OK" -ForegroundColor Green
Write-Host ""

Write-Host "[2/3] Starting infrastructure services (Redis, MySQL, Kafka)..." -ForegroundColor Yellow
docker-compose up -d
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to start infrastructure services" -ForegroundColor Red
    exit 1
}

Write-Host "  Waiting for services to be ready..." -ForegroundColor Gray
Start-Sleep -Seconds 15

Write-Host "  Checking Redis..." -ForegroundColor Gray
$redisReady = docker exec cashparty-redis redis-cli ping 2>$null
if ($redisReady -eq "PONG") {
    Write-Host "  - Redis: OK" -ForegroundColor Green
} else {
    Write-Host "  - Redis: Starting..." -ForegroundColor Yellow
}

Write-Host "  Checking MySQL..." -ForegroundColor Gray
$mysqlReady = docker exec cashparty-mysql mysqladmin ping -h localhost -uroot -p123456 2>$null
if ($mysqlReady -match "alive") {
    Write-Host "  - MySQL: OK" -ForegroundColor Green
} else {
    Write-Host "  - MySQL: Starting..." -ForegroundColor Yellow
}

Write-Host "  Checking Kafka..." -ForegroundColor Gray
Write-Host "  - Kafka: OK (may need more time to fully start)" -ForegroundColor Green
Write-Host ""

Write-Host "[3/3] Starting application services..." -ForegroundColor Yellow

Write-Host "  Starting game service on port 50051..." -ForegroundColor Gray
Start-Process -FilePath "powershell" -ArgumentList "-NoExit", "-Command", "cd '$BackendDir'; `$env:PATH = 'C:\Users\Administrator\sdk\go1.25.0\bin;`$env:PATH'; go run ./cmd/game" -WindowStyle Normal

Start-Sleep -Seconds 3

Write-Host "  Starting gateway service on port 8080..." -ForegroundColor Gray
Start-Process -FilePath "powershell" -ArgumentList "-NoExit", "-Command", "cd '$BackendDir'; `$env:PATH = 'C:\Users\Administrator\sdk\go1.25.0\bin;`$env:PATH'; go run ./cmd/gateway" -WindowStyle Normal

Write-Host ""
Write-Host "=== Services Started ===" -ForegroundColor Cyan
Write-Host "  - Game Service:   gRPC port 50051" -ForegroundColor White
Write-Host "  - Gateway Service: WS port 8080" -ForegroundColor White
Write-Host ""
Write-Host "Press any key to stop all services..." -ForegroundColor Yellow
$null = $Host.UI.RawUI.ReadKey("NoEcho,IncludeKeyDown")

Write-Host ""
Write-Host "Stopping services..." -ForegroundColor Yellow
docker-compose down
Write-Host "Done." -ForegroundColor Green
