param()

git fetch --tags --force origin 2>$null

$localTags = @(git tag -l)
$remoteTags = @(git ls-remote --tags origin 2>$null | ForEach-Object {
    $parts = $_ -split '\s+'
    if ($parts.Length -gt 1) {
        $parts[1] -replace '^refs/tags/', '' -replace '\^\{\}$', ''
    }
})

$allTags = @($localTags + $remoteTags | Where-Object { $_ -ne $null -and $_ -ne '' } | Select-Object -Unique)

$maxVersion = [version]'0.0.0'
foreach ($t in $allTags) {
    $clean = $t -replace '^v\.?', ''
    if ($clean -match '^(\d+)\.(\d+)(?:\.(\d+))?') {
        $major = [int]$matches[1]
        $minor = [int]$matches[2]
        $build = 0
        if ($matches[3]) {
            $build = [int]$matches[3]
        }
        $parsed = [version]("{0}.{1}.{2}" -f $major, $minor, $build)
        if ($parsed -gt $maxVersion) {
            $maxVersion = $parsed
        }
    }
}

if ($maxVersion -eq [version]'0.0.0') {
    $maxVersion = [version]'1.0.0'
}

$curTag = "v" + $maxVersion.ToString(3)
$nextPatch = $maxVersion.Build + 1
$newTag = "v{0}.{1}.{2}" -f $maxVersion.Major, $maxVersion.Minor, $nextPatch

while ($allTags -contains $newTag) {
    $nextPatch++
    $newTag = "v{0}.{1}.{2}" -f $maxVersion.Major, $maxVersion.Minor, $nextPatch
}

Write-Output "$curTag $newTag"
