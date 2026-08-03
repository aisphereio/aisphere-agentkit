param(
  [string]$BaseUrl = "http://localhost:8080/api",
  [Parameter(Mandatory=$true)][string]$UploadId,
  [Parameter(Mandatory=$true)][string]$AppName,
  [Parameter(Mandatory=$true)][string]$SessionId,
  [string]$ArtifactName = ""
)

$ErrorActionPreference = "Stop"
$body = @{
  app_name = $AppName
  session_id = $SessionId
}
if ($ArtifactName) { $body.artifact_name = $ArtifactName }

Invoke-RestMethod `
  -Method Post `
  -Uri "$BaseUrl/platform/uploads/$UploadId/attach-artifact" `
  -ContentType "application/json" `
  -Body ($body | ConvertTo-Json -Depth 10)
