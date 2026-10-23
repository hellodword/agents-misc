param(
    [Parameter(Mandatory=$true)][string]$Python,
    [Parameter(Mandatory=$true)][string]$Destination,
    [string]$Uv = 'uv'
)
$ErrorActionPreference = 'Stop'
if (-not [IO.Path]::IsPathRooted($Python) -or -not [IO.Path]::IsPathRooted($Destination)) {
    throw 'Python and Destination must be absolute paths'
}
function Invoke-Checked([string[]]$Arguments) {
    & $Uv @Arguments
    if ($LASTEXITCODE -ne 0) { throw "uv failed: $Arguments" }
}
& $Python -c "import struct,sys; assert sys.version_info[:3] == (3,11,17) and struct.calcsize('P') == 8; print(sys.version)"
if ($LASTEXITCODE -ne 0) { throw 'Expected Windows amd64 Python 3.11.17' }
$assets = Join-Path $PSScriptRoot '..\assets\desktop-example'
foreach ($name in @('controls','vision')) {
    $environment = Join-Path $Destination $name
    $interpreter = Join-Path $environment 'Scripts\python.exe'
    if (-not (Test-Path $interpreter)) {
        Invoke-Checked -Arguments @('venv','--python',$Python,$environment)
    }
    Invoke-Checked -Arguments @('pip','sync','--python',$interpreter,'--require-hashes',(Join-Path $assets "$name.txt"))
    Invoke-Checked -Arguments @('pip','check','--python',$interpreter)
    & $interpreter -c 'import platform,sys; print(sys.executable); print(platform.python_version())'
    if ($LASTEXITCODE -ne 0) { throw "Interpreter self-check failed: $name" }
}
& (Join-Path $Destination 'controls\Scripts\python.exe') -c 'import pywinauto, PIL.ImageGrab, psutil, win32job; print(pywinauto.__version__)'
if ($LASTEXITCODE -ne 0) { throw 'Controls imports failed' }
& (Join-Path $Destination 'vision\Scripts\python.exe') -c 'from airtest.core.cv import Template; import numpy,cv2; print(numpy.__version__, cv2.__version__)'
if ($LASTEXITCODE -ne 0) { throw 'Vision imports failed' }
