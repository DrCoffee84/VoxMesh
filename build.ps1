$ErrorActionPreference = "Stop"

if (Test-Path "C:\msys64\ucrt64\bin\gcc.exe") {
    $env:CC = "C:\msys64\ucrt64\bin\gcc.exe"
    $env:Path = "C:\msys64\ucrt64\bin;$env:Path"
} elseif (Test-Path "C:\msys64\mingw64\bin\gcc.exe") {
    $env:CC = "C:\msys64\mingw64\bin\gcc.exe"
    $env:Path = "C:\msys64\mingw64\bin;$env:Path"
}

$env:CGO_ENABLED = "1"

if (Test-Path "icon.png") {
    Copy-Item "icon.png" "internal\ui\icon.png" -Force
}

if (Test-Path "icon.ico") {
    & windres -i cmd\voxmesh\voxmesh.rc -O coff -F pe-x86-64 -o cmd\voxmesh\voxmesh_windows_amd64.syso
}

if (-not (Test-Path "dist")) {
    New-Item -ItemType Directory -Path "dist" | Out-Null
}

Remove-Item .\dist\voxmesh.exe -ErrorAction SilentlyContinue

$build = (Get-Date -Format "yyyyMMdd-HHmm")
# Versión = último tag + "-dev": el updater corta en "-", así que un build local vale lo mismo que
# su release y no ofrece "actualizar" a él (con "0.9.5_fecha" lo ofrecía siempre y, si se
# aceptaba, reemplazaba el build por el release y lo relanzaba).
$tag = (git describe --tags --abbrev=0 2>$null)
if (-not $tag) { $tag = "v0.0.0" }
$version = "$($tag.TrimStart('v'))-dev.$build"
Write-Host "Compilando VoxMesh con CGO y Fyne UI..." -ForegroundColor Cyan

go build -trimpath -tags nolibopusfile -ldflags "-s -w -H windowsgui -linkmode external -extldflags -static -X voxmesh/internal/version.Value=$version" -o dist/voxmesh.exe ./cmd/voxmesh

if ($LASTEXITCODE -eq 0) {
    $size = (Get-Item .\dist\voxmesh.exe).Length / 1MB
    Write-Host ("Build completado exitosamente: dist/voxmesh.exe ({0:N2} MB)" -f $size) -ForegroundColor Green
} else {
    Write-Host "Error en la compilacion." -ForegroundColor Red
}
