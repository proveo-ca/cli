//go:build e2e

// SPEC: _spec/internal/sbx/host-android-adb.puml, _spec/internal/sbx/mobile-device-reach.puml

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/proveo-ca/proveo/internal/hostdev"
	"github.com/proveo-ca/proveo/internal/imagetest"
	"github.com/proveo-ca/proveo/internal/sbx"
)

// guestDeviceWait bounds the guest's retries for a row in state "device".
const guestDeviceWait = 60 * time.Second

func TestHostADBReachesTheHostEmulatorFromTheSandbox(t *testing.T) {
	if _, err := exec.LookPath(sbx.Binary); err != nil {
		t.Skipf("%s not on PATH", sbx.Binary)
	}
	if ok, why := sbx.Available(); !ok {
		t.Skipf("sbx unavailable: %s", why)
	}
	if hostdev.EmulatorBinary(os.Getenv) == "" || hostdev.AdbBinary(os.Getenv) == "" {
		t.Skip("no Android SDK emulator/adb on this host")
	}
	image := imagetest.PinnedRef("claudecode", "proveo/claudecode:local")
	if !dockerImagePresent(t, image) {
		t.Skipf("image %s not built (mise run build claudecode)", image)
	}
	port, err := hostdev.ADBPort(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}

	booted := !hostdev.ADBReady(port)
	if _, err := hostdev.EnsureADB(os.Getenv, port, t.Logf); err != nil {
		t.Skipf("host emulator: %v", err)
	}
	if booted {
		t.Cleanup(func() { stopEmulators(t, port) })
	}

	name := fmt.Sprintf("proveo-adbsbx-%d", time.Now().UnixNano())
	createWorkloadSandbox(t, sbx.RunConfig{Name: name, KitDir: probeWorkload(t, "proveo-adbsbx", sbx.RecipeV3{From: image}),
		Mounts: mountsOf(t.TempDir())})
	if out, err := exec.Command(sbx.Binary, "policy", "allow", "network", "--sandbox", name,
		hostdev.PolicyHost(port)).CombinedOutput(); err != nil {
		t.Fatalf("policy allow: %v\n%s", err, out)
	}

	// The working-tree lib, not the baked one: the image lags the source until a rebuild.
	lib := filepath.Join(repoRoot(t), "packages", "lib", "entrypoint-lib.sh")
	if out, err := exec.Command(sbx.Binary, "cp", lib, name+":/tmp/entrypoint-lib.sh").CombinedOutput(); err != nil {
		t.Fatalf("sbx cp: %v\n%s", err, out)
	}

	probe := fmt.Sprintf(`set -e
export HOME=/tmp %s=%d
source /tmp/entrypoint-lib.sh
proveo_host_adb_env
echo "adb_host=$ADB_HOST adb_port=$ADB_PORT"
deadline=$((SECONDS + %d))
tries=0
while :; do
  tries=$((tries + 1))
  if body=$(adb -H "$ADB_HOST" -P "$ADB_PORT" devices 2>&1); then status=OKAY; else status=error; fi
  [[ "$status" == OKAY && "$body" == *$'\tdevice'* ]] && break
  (( SECONDS >= deadline )) && break
  sleep 2
done
printf 'tries=%%s\nstatus=%%s\ndevices=%%s\n' "$tries" "$status" "${body//$'\n'/;}"
`, hostdev.EnvADBPort, port, int(guestDeviceWait/time.Second))
	ob, err := exec.Command(sbx.Binary, "exec", "-w", "/", name, "--", "bash", "-c", probe).CombinedOutput()
	out := string(ob)
	if err != nil {
		t.Fatalf("probe exec: %v\n%s", err, out)
	}
	if !strings.Contains(out, "status=OKAY") || !strings.Contains(out, "\tdevice") {
		t.Fatalf("the sandbox did not see a usable device on the host adb server:\n%s", out)
	}
	if !strings.Contains(out, "adb_port="+strconv.Itoa(port)) || strings.Contains(out, "adb_host= ") {
		t.Fatalf("proveo_host_adb_env did not export the gateway address:\n%s", out)
	}
	t.Logf("guest view: %s", strings.TrimSpace(out))

	session := fmt.Sprintf(`set -e
export HOME=/tmp %s=%d
source /tmp/entrypoint-lib.sh
proveo_host_adb_env
proveo_adb_mirror_start
node --input-type=module - <<'JS'
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
const child = spawn("bash", ["-c", "source /tmp/entrypoint-lib.sh && proveo_mobile_mcp_exec"], { stdio: ["pipe", "pipe", "ignore"] });
const lines = createInterface({ input: child.stdout })[Symbol.asyncIterator]();
const send = msg => child.stdin.write(JSON.stringify({ jsonrpc: "2.0", ...msg }) + "\n");
async function call(id, method, params) {
  send({ id, method, params });
  for (;;) {
    const line = await lines.next();
    if (line.done) throw Error("no reply to " + method);
    const msg = JSON.parse(line.value);
    if (msg.id === id) return msg.result;
  }
}
async function tool(id, name, args) {
  const result = await call(id, "tools/call", { name, arguments: args });
  const content = result.content || [];
  const kinds = content.map(item => item.type).join(",");
  const text = content.filter(item => item.type === "text").map(item => item.text ?? "").join(" ");
  console.log(name + " error=" + (result.isError ? "True" : "False") + " kinds=" + kinds + " text=" + text.slice(0, 160).replaceAll("\n", " "));
  return text;
}
try {
  await call(1, "initialize", { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "proveo", version: "1" } });
  send({ method: "notifications/initialized" });
  const device = JSON.parse(await tool(2, "mobile_list_available_devices", {})).devices[0].id;
  await tool(3, "mobile_launch_app", { device, packageName: "com.android.settings" });
  await tool(4, "mobile_list_elements_on_screen", { device });
  await tool(5, "mobile_click_on_screen_at_coordinates", { device, x: 540, y: 640 });
  await tool(6, "mobile_take_screenshot", { device });
  await tool(7, "mobile_press_button", { device, button: "HOME" });
} finally {
  child.kill();
}
JS
`, hostdev.EnvADBPort, port)
	ob, err = exec.Command(sbx.Binary, "exec", "-w", "/", name, "--", "bash", "-c", session).CombinedOutput()
	out = string(ob)
	if err != nil {
		t.Fatalf("mobile-mcp session: %v\n%s", err, out)
	}
	for _, want := range []string{
		"mobile_launch_app error=False",
		"mobile_list_elements_on_screen error=False",
		"mobile_click_on_screen_at_coordinates error=False",
		"mobile_take_screenshot error=False kinds=text,image",
		"mobile_press_button error=False",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mobile-mcp session missing %q:\n%s", want, out)
		}
	}
	t.Logf("mobile-mcp session:\n%s", strings.TrimSpace(out))
	if !strings.Contains(out, "@e1") {
		t.Errorf("list_elements returned no element refs — the forwarded device server is unreachable:\n%s", out)
	}
}
