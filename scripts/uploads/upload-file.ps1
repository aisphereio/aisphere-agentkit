param(
  [string]$BaseUrl = "http://localhost:8080/api",
  [Parameter(Mandatory=$true)][string]$File,
  [string]$Purpose = "general",
  [string]$AppName = "",
  [string]$SessionId = "",
  [string]$ProjectId = ""
)

$ErrorActionPreference = "Stop"
if (!(Test-Path $File)) {
  throw "File not found: $File"
}

$form = @{
  file = Get-Item $File
  purpose = $Purpose
}
if ($AppName) { $form.app_name = $AppName }
if ($SessionId) { $form.session_id = $SessionId }
if ($ProjectId) { $form.project_id = $ProjectId }

Invoke-RestMethod `
  -Method Post `
  -Uri "$BaseUrl/platform/uploads" `
  -Form $form
