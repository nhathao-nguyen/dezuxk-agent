param(
    [string]$Prompt = "Hello! Who are you?",
    [string]$Model = "gemini-3.8-flash"
)

$headers = @{
    Authorization = "Bearer sk-dez-12f564ddef831e78546a198cd56f4deb"
    "Content-Type" = "application/json; charset=utf-8"
}

$payload = @{
    model = $Model
    messages = @(
        @{ role = "user"; content = $Prompt }
    )
} | ConvertTo-Json -Depth 5

Write-Host "Sending request to Dezuxk AI Gateway ($Model)..." -ForegroundColor Cyan
try {
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $res = Invoke-RestMethod -Uri "http://127.0.0.1:8080/v1/chat/completions" -Method Post -Headers $headers -Body $bytes -TimeoutSec 60
    Write-Host "=== Gemini Response ===" -ForegroundColor Green
    Write-Host $res.choices[0].message.content
} catch {
    Write-Host "Request Failed" -ForegroundColor Red
}
