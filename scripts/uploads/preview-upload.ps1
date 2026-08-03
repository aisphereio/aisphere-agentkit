param(
  [string]$BaseUrl = "http://localhost:8080/api",
  [Parameter(Mandatory=$true)][string]$UploadId,
  [int]$MaxBytes = 8192
)

$ErrorActionPreference = "Stop"
Invoke-RestMethod `
  -Method Get `
  -Uri "$BaseUrl/platform/uploads/$UploadId/preview?max_bytes=$MaxBytes"
