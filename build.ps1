$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path output | Out-Null
go build -o output/casehub.exe .
Write-Host "Built output/casehub.exe"
