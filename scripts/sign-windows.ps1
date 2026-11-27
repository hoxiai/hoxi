#Requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory = $true)][string]$Path)

$ErrorActionPreference = 'Stop'
$thumbprint = ([string]$env:HOXI_WINDOWS_SIGN_THUMBPRINT).Trim()
if (!$thumbprint) { return } # Developer builds do not require a certificate.
if ($thumbprint -notmatch '^[A-Fa-f0-9]{40}$') {
  throw 'HOXI_WINDOWS_SIGN_THUMBPRINT must identify a certificate in the current user certificate store'
}

$target = (Resolve-Path -LiteralPath $Path).ProviderPath
$signTool = (Get-Command signtool.exe -CommandType Application -ErrorAction Stop).Source
& $signTool sign /sha1 $thumbprint /fd SHA256 /tr https://timestamp.digicert.com /td SHA256 $target
if ($LASTEXITCODE -ne 0) { throw 'SignTool failed to sign the artifact' }
$signature = Get-AuthenticodeSignature -LiteralPath $target
if ($signature.Status -ne 'Valid' -or $signature.SignatureType -ne 'Authenticode' -or
    $signature.SignerCertificate.Thumbprint -ne $thumbprint -or !$signature.TimeStamperCertificate) {
  throw 'Could not verify the expected timestamped Authenticode signature'
}
