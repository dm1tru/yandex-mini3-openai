#!/bin/sh
# Run only in a disposable container, never on the speaker/host:
# docker run --rm --network none -v "$PWD:/src:ro" -w /src busybox:1.36 sh tests/test-network-config.sh
set -eu
test -f /src/S98mini3wg.v2
mkdir -p /data/opt/bin /data/opt/etc/wireguard /etc/init.d
export CALLS=/tmp/mock-ip-calls
: > "$CALLS"
cat > /data/opt/bin/ip <<'MOCK'
#!/bin/sh
echo "$*" >> "$CALLS"
case "$*" in
    '-4 addr show wlan-test') echo 'inet 198.51.100.2/24';;
esac
MOCK
chmod 755 /data/opt/bin/ip
for script in S98mini3wg.v2 mini3-dhcpcd.exit-hook gpt-live vosk-live vosk-test build.sh fetch-model.sh; do
    sh -n "/src/$script"
done
if MINI3_WG_CONFIG=/tmp/does-not-exist sh /src/S98mini3wg.v2 refresh 2>/dev/null; then
    echo 'Missing configuration was accepted'; exit 1
fi
test ! -s "$CALLS"
cp /src/config/network.conf.example /tmp/network.conf
sed 's/ENDPOINT=203.0.113.10/ENDPOINT=999.0.0.1/' /tmp/network.conf > /tmp/invalid.conf
if MINI3_WG_CONFIG=/tmp/invalid.conf sh /src/S98mini3wg.v2 refresh 2>/dev/null; then
    echo 'Invalid IPv4 was accepted'; exit 1
fi
test ! -s "$CALLS"
sed 's/LAN_INTERFACE=wlan0/LAN_INTERFACE=wlan-test/' /tmp/network.conf > /tmp/valid.conf
MINI3_WG_CONFIG=/tmp/valid.conf sh /src/S98mini3wg.v2 refresh
grep -Fx 'route replace 198.51.100.0/24 dev wlan-test table 200' "$CALLS"
grep -Fx 'route replace default via 198.51.100.1 dev wlan-test table 200' "$CALLS"
grep -Fx 'rule add priority 100 to 203.0.113.10/32 lookup 200' "$CALLS"
grep -Fx 'rule add priority 101 to 203.0.113.123/32 lookup 200' "$CALLS"
grep -Fx 'nameserver 1.1.1.1' /tmp/resolv.conf
for invalid in 'ENDPOINT_PORT=70000' 'MTU=1' 'VPN_ADDRESS=192.0.2.2/99' 'LAN_INTERFACE=bad/interface'; do
    cp /tmp/valid.conf /tmp/invalid.conf
    echo "$invalid" >> /tmp/invalid.conf
    : > "$CALLS"
    if MINI3_WG_CONFIG=/tmp/invalid.conf sh /src/S98mini3wg.v2 refresh 2>/dev/null; then
        echo 'Invalid setting was accepted'; exit 1
    fi
    test ! -s "$CALLS"
done
cp /tmp/valid.conf /data/opt/etc/wireguard/network.conf
cat > /etc/init.d/S98mini3wg <<'MOCK'
#!/bin/sh
echo "$*" >> /tmp/hook-calls
MOCK
chmod 755 /etc/init.d/S98mini3wg
: > /tmp/hook-calls
interface=other reason=BOUND sh -c '. /src/mini3-dhcpcd.exit-hook'
test ! -s /tmp/hook-calls
interface=wlan-test reason=BOUND sh -c '. /src/mini3-dhcpcd.exit-hook'
grep -Fx refresh /tmp/hook-calls
echo 'Network configuration and hook tests passed (mock commands, no network).'
