$ErrorActionPreference='Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try { node tools/task.mjs test; if ($LASTEXITCODE -ne 0) { throw 'Task failed' } } finally { Pop-Location }
