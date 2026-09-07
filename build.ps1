$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path build | Out-Null
go build -o build/casehub.exe .
Write-Host "Built build/casehub.exe"
