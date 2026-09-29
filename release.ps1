param (
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Tag,

    [Parameter(Mandatory = $false, Position = 1)]
    [string]$Message = "Release $Tag"
)

$ErrorActionPreference = "Stop"

if (-not $Tag.StartsWith("v")) {
    $Tag = "v$Tag"
}

Write-Host "Creando release con tag: $Tag..." -ForegroundColor Cyan

# Comprobar si hay cambios sin commitear
$status = git status --porcelain
if ($status) {
    Write-Host "ADVERTENCIA: Hay cambios sin confirmar en el repositorio local." -ForegroundColor Yellow
}

# Crear tag git
git tag -a $Tag -m $Message
Write-Host "Tag $Tag creado localmente." -ForegroundColor Green

# Pushear tag a GitHub
Write-Host "Pusheando tag a GitHub para disparar el pipeline de compilación..." -ForegroundColor Cyan
git push origin $Tag

Write-Host ""
Write-Host "¡Tag enviado exitosamente!" -ForegroundColor Green
Write-Host "GitHub Actions compilará automáticamente 'voxmesh.exe' y 'voxmesh-mic.apk' y creará la Release pública." -ForegroundColor White
Write-Host "Podés ver el progreso en: https://github.com/DrCoffee84/VoxMesh/actions" -ForegroundColor Yellow
