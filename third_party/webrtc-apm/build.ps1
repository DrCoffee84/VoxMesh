# Compila WebRTC Audio Processing 1.3 (cancelación de eco AEC3) como biblioteca estática en
# third_party/webrtc-apm/out, para que cgo la meta dentro de voxmesh.exe (internal/audio/aec_windows.go).
# MSYS2 solo la empaqueta como DLL: se compila con su misma receta (tarball + 3 parches) y
# --default-library=static. Necesita MSYS2 UCRT64 con:
#   pacman -S mingw-w64-ucrt-x86_64-gcc mingw-w64-ucrt-x86_64-meson mingw-w64-ucrt-x86_64-ninja `
#             mingw-w64-ucrt-x86_64-pkgconf mingw-w64-ucrt-x86_64-abseil-cpp
# Uso: .\third_party\webrtc-apm\build.ps1 [-Ucrt C:\msys64\ucrt64] [-Force]
# No hace nada si out/ ya tiene esta versión compilada contra el abseil instalado.
param(
    [string]$Ucrt = "C:\msys64\ucrt64",
    [switch]$Force
)
$ErrorActionPreference = "Stop"

$version = "1.3"
$url = "https://gitlab.freedesktop.org/pulseaudio/webrtc-audio-processing/-/archive/v$version/webrtc-audio-processing-v$version.tar.gz"
$sha256 = "b04404884e705464c1a2da0d5036ae0fc02d87ed150faa7da4536c99d6966660"

$here = $PSScriptRoot
$out = Join-Path $here "out"
$work = Join-Path $here "work"
$lib = Join-Path $out "lib\libwebrtc-audio-processing-1.a"

if (-not (Test-Path "$Ucrt\bin\gcc.exe")) { throw "No encuentro gcc en $Ucrt\bin (MSYS2 UCRT64)" }
$absl = Get-Content "$Ucrt\lib\pkgconfig\absl_base.pc" -ErrorAction SilentlyContinue |
    Where-Object { $_ -like "Version:*" } | Select-Object -First 1
if (-not $absl) { throw "Falta abseil-cpp: pacman -S mingw-w64-ucrt-x86_64-abseil-cpp" }
# El .a queda atado a los headers de abseil con los que se compiló: si pacman lo actualiza, se
# vuelve a compilar.
$patches = Get-ChildItem (Join-Path $here "patches") -Filter *.patch | Sort-Object Name
$stamp = "webrtc-audio-processing $version | $Ucrt | absl $($absl.Substring(8).Trim()) | " +
    (($patches | ForEach-Object { (Get-FileHash $_.FullName -Algorithm SHA256).Hash.Substring(0, 12) }) -join ",")
$stampFile = Join-Path $out "stamp.txt"
if (-not $Force -and (Test-Path $lib) -and (Test-Path $stampFile) -and ((Get-Content $stampFile -Raw).Trim() -eq $stamp)) {
    Write-Host "WebRTC APM al dia ($stamp)"
    exit 0
}

$env:Path = "$Ucrt\bin;$env:Path"
$env:CC = "$Ucrt\bin\gcc.exe"
$env:CXX = "$Ucrt\bin\g++.exe"

Remove-Item $work, $out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $work -Force | Out-Null

Write-Host "Bajando webrtc-audio-processing $version..." -ForegroundColor Cyan
$tarball = Join-Path $work "src.tar.gz"
# curl.exe (viene con Windows 10+): a Invoke-WebRequest GitLab le contesta una página anti-bot.
& curl.exe -fsSL -o $tarball $url
if ($LASTEXITCODE -ne 0) { throw "No se pudo bajar $url" }
$got = (Get-FileHash $tarball -Algorithm SHA256).Hash.ToLower()
if ($got -ne $sha256) { throw "El SHA-256 del tarball no coincide: $got" }

& tar -xzf $tarball -C $work
if ($LASTEXITCODE -ne 0) { throw "tar fallo" }
$src = Join-Path $work "webrtc-audio-processing-v$version"
# Sin el techo, git encuentra el repo de VoxMesh de arriba y saltea en silencio los parches
# (rutas fuera del subdirectorio): el build falla después con errores que no dicen por qué.
$env:GIT_CEILING_DIRECTORIES = $work
foreach ($patch in $patches) {
    & git -C $src apply -p1 $patch.FullName
    if ($LASTEXITCODE -ne 0) { throw "No se pudo aplicar $($patch.Name)" }
}

Write-Host "Compilando (estatica, release)..." -ForegroundColor Cyan
$build = Join-Path $work "build"
& meson setup $build $src "--prefix=$out" --wrap-mode=nodownload --default-library=static --buildtype=release -Db_ndebug=true
if ($LASTEXITCODE -ne 0) { throw "meson setup fallo" }
& meson compile -C $build
if ($LASTEXITCODE -ne 0) { throw "meson compile fallo" }
& meson install -C $build
if ($LASTEXITCODE -ne 0) { throw "meson install fallo" }
Copy-Item (Join-Path $src "COPYING") (Join-Path $out "COPYING")

Set-Content -Path $stampFile -Value $stamp -Encoding ascii
Remove-Item $work -Recurse -Force
Write-Host "WebRTC APM listo: $lib" -ForegroundColor Green
