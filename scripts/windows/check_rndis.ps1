# Checks for RNDIS adapter presence on Windows and optionally installs driver.
# Run as Administrator.

$ErrorActionPreference = "Stop"

Write-Host "=== filetrans RNDIS Check ===" -ForegroundColor Cyan

# Look for RNDIS or USB Ethernet adapters
$adapters = Get-NetAdapter | Where-Object {
    $_.InterfaceDescription -match "RNDIS|USB Ethernet|Linux" -or
    $_.Name -match "RNDIS|USB"
}

if ($adapters) {
    Write-Host "RNDIS/USB adapters found:" -ForegroundColor Green
    $adapters | Format-Table Name, InterfaceDescription, Status, LinkSpeed -AutoSize

    foreach ($adapter in $adapters) {
        $adapterName = $adapter.Name
        Write-Host "`nConfiguring static IP on '$adapterName'..." -ForegroundColor Yellow

        try {
            # Remove existing IP on the interface
            $existing = Get-NetIPAddress -InterfaceAlias $adapterName -AddressFamily IPv4 -ErrorAction SilentlyContinue
            if ($existing) {
                Remove-NetIPAddress -InterfaceAlias $adapterName -AddressFamily IPv4 -Confirm:$false
            }

            New-NetIPAddress `
                -InterfaceAlias $adapterName `
                -IPAddress "192.168.7.2" `
                -PrefixLength 24 `
                -ErrorAction Stop | Out-Null

            Write-Host "  Assigned 192.168.7.2/24 to '$adapterName'" -ForegroundColor Green
        } catch {
            Write-Host "  Failed to assign IP: $_" -ForegroundColor Red
            Write-Host "  Try: Run this script as Administrator" -ForegroundColor Yellow
        }
    }
} else {
    Write-Host "No RNDIS/USB Ethernet adapter detected." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Possible causes:" -ForegroundColor White
    Write-Host "  1. Linux gadget not enabled — run scripts/linux/setup_gadget.sh on the Linux side first"
    Write-Host "  2. USB cable not connected"
    Write-Host "  3. Driver not installed — check Device Manager for unknown devices"
    Write-Host ""
    Write-Host "To manually install RNDIS driver:" -ForegroundColor White
    Write-Host "  Device Manager → Unknown Device → Update Driver →"
    Write-Host "  Browse → Let me pick → Network Adapters → Microsoft → Remote NDIS Compatible Device"
}

# Test connectivity if IP was assigned
$ping = Test-NetConnection -ComputerName "192.168.7.1" -Port 7070 -WarningAction SilentlyContinue -ErrorAction SilentlyContinue
if ($ping.TcpTestSucceeded) {
    Write-Host "`nPeer reachable at 192.168.7.1:7070 — ready to transfer!" -ForegroundColor Green
} else {
    Write-Host "`nPeer not yet reachable at 192.168.7.1:7070 (start filetrans on Linux side first)" -ForegroundColor DarkYellow
}
