//go:build windows

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type windowsRoute struct {
	InterfaceIndex int    `json:"InterfaceIndex"`
	NextHop        string `json:"NextHop"`
}

func SetupClient(ctx context.Context, o ClientOptions) (Cleanup, error) {
	dns := make([]string, 0, len(o.DNS))
	for _, ip := range o.DNS {
		dns = append(dns, ip.String())
	}
	env := append(os.Environ(),
		"VEILINK_TUN="+o.Interface,
		"VEILINK_ADDRESS="+o.Address.String(),
		"VEILINK_GATEWAY="+o.Gateway.String(),
		"VEILINK_SERVER_IP="+o.ServerIP.String(),
		"VEILINK_MTU="+fmt.Sprint(o.MTU),
		"VEILINK_DNS="+strings.Join(dns, ","),
	)
	script := `$ErrorActionPreference='Stop'
$tun=Get-NetAdapter -Name $env:VEILINK_TUN -ErrorAction Stop
$orig=Get-NetRoute -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' | Where-Object {$_.InterfaceIndex -ne $tun.ifIndex -and $_.NextHop -ne '0.0.0.0'} | Sort-Object RouteMetric,InterfaceMetric | Select-Object -First 1
if(-not $orig){throw 'No usable IPv4 default route found'}
try {
  $ErrorActionPreference='SilentlyContinue'
  Remove-NetRoute -DestinationPrefix ($env:VEILINK_SERVER_IP+'/32') -InterfaceIndex $orig.InterfaceIndex -NextHop $orig.NextHop -Confirm:$false
  '0.0.0.0/1','128.0.0.0/1',($env:VEILINK_GATEWAY+'/32'),'::/1','8000::/1' | ForEach-Object {Remove-NetRoute -DestinationPrefix $_ -InterfaceIndex $tun.ifIndex -Confirm:$false}
  Remove-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress $env:VEILINK_ADDRESS -Confirm:$false
  $ErrorActionPreference='Stop'
  Set-NetIPInterface -InterfaceIndex $tun.ifIndex -AddressFamily IPv4 -NlMtuBytes ([int]$env:VEILINK_MTU)
  New-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress $env:VEILINK_ADDRESS -PrefixLength 32 -AddressFamily IPv4 -ErrorAction Stop | Out-Null
  New-NetRoute -DestinationPrefix ($env:VEILINK_SERVER_IP+'/32') -InterfaceIndex $orig.InterfaceIndex -NextHop $orig.NextHop -RouteMetric 1 -ErrorAction Stop | Out-Null
  New-NetRoute -DestinationPrefix ($env:VEILINK_GATEWAY+'/32') -InterfaceIndex $tun.ifIndex -NextHop '0.0.0.0' -RouteMetric 1 -ErrorAction Stop | Out-Null
  New-NetRoute -DestinationPrefix '0.0.0.0/1' -InterfaceIndex $tun.ifIndex -NextHop '0.0.0.0' -RouteMetric 5 -ErrorAction Stop | Out-Null
  New-NetRoute -DestinationPrefix '128.0.0.0/1' -InterfaceIndex $tun.ifIndex -NextHop '0.0.0.0' -RouteMetric 5 -ErrorAction Stop | Out-Null
  if($env:VEILINK_BLOCK_IPV6 -eq '1'){
    New-NetRoute -DestinationPrefix '::/1' -InterfaceIndex $tun.ifIndex -NextHop '::' -RouteMetric 5 -ErrorAction Stop | Out-Null
    New-NetRoute -DestinationPrefix '8000::/1' -InterfaceIndex $tun.ifIndex -NextHop '::' -RouteMetric 5 -ErrorAction Stop | Out-Null
  }
  if($env:VEILINK_DNS){Set-DnsClientServerAddress -InterfaceIndex $tun.ifIndex -ServerAddresses ($env:VEILINK_DNS -split ',')}
  [pscustomobject]@{InterfaceIndex=$orig.InterfaceIndex;NextHop=$orig.NextHop}|ConvertTo-Json -Compress
} catch {
  $failure=$_
  $ErrorActionPreference='SilentlyContinue'
  Remove-NetRoute -DestinationPrefix ($env:VEILINK_SERVER_IP+'/32') -InterfaceIndex $orig.InterfaceIndex -NextHop $orig.NextHop -Confirm:$false
  '0.0.0.0/1','128.0.0.0/1',($env:VEILINK_GATEWAY+'/32'),'::/1','8000::/1' | ForEach-Object {Remove-NetRoute -DestinationPrefix $_ -InterfaceIndex $tun.ifIndex -Confirm:$false}
  Remove-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress $env:VEILINK_ADDRESS -Confirm:$false
  Set-DnsClientServerAddress -InterfaceIndex $tun.ifIndex -ResetServerAddresses
  throw $failure
}`
	if o.BlockIPv6 {
		env = append(env, "VEILINK_BLOCK_IPV6=1")
	}
	out, err := powershell(ctx, env, script)
	if err != nil {
		return nil, fmt.Errorf("configure Windows routes (run elevated): %w", err)
	}
	var orig windowsRoute
	if err := json.Unmarshal(out, &orig); err != nil {
		return nil, fmt.Errorf("decode original Windows route: %w", err)
	}
	cleanup := func(cleanCtx context.Context) error {
		cleanupEnv := append(env, "VEILINK_ORIG_IF="+fmt.Sprint(orig.InterfaceIndex), "VEILINK_ORIG_GW="+orig.NextHop)
		cleanupScript := `$ErrorActionPreference='SilentlyContinue'
$tun=Get-NetAdapter -Name $env:VEILINK_TUN
Remove-NetRoute -DestinationPrefix ($env:VEILINK_SERVER_IP+'/32') -InterfaceIndex ([int]$env:VEILINK_ORIG_IF) -NextHop $env:VEILINK_ORIG_GW -Confirm:$false
if($tun){
  '0.0.0.0/1','128.0.0.0/1',($env:VEILINK_GATEWAY+'/32'),'::/1','8000::/1' | ForEach-Object {Remove-NetRoute -DestinationPrefix $_ -InterfaceIndex $tun.ifIndex -Confirm:$false}
  Remove-NetIPAddress -InterfaceIndex $tun.ifIndex -IPAddress $env:VEILINK_ADDRESS -Confirm:$false
  Set-DnsClientServerAddress -InterfaceIndex $tun.ifIndex -ResetServerAddresses
}`
		_, cleanErr := powershell(cleanCtx, cleanupEnv, cleanupScript)
		return cleanErr
	}
	return cleanup, nil
}

func SetupServer(context.Context, ServerOptions) (Cleanup, error) {
	return nil, errors.New("server mode is supported on Linux VDS only")
}

func powershell(ctx context.Context, env []string, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
