package cloudexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentic-ai-backend/internal/jobspec"
)

// Real dependency installation through the real registry proxy. These need the
// internet, CLOUDEXEC_DOCKER_TESTS=1 and a network made by
// deploy/registry-proxy/setup.sh (CLOUDEXEC_INSTALL_NETWORK names it).

func installExecutor(t *testing.T) *DockerExecutor {
	t.Helper()
	executor := realExecutor(t)
	network := os.Getenv("CLOUDEXEC_INSTALL_NETWORK")
	if network == "" {
		t.Skip("set CLOUDEXEC_INSTALL_NETWORK to the network made by deploy/registry-proxy/setup.sh")
	}
	executor.Config.InstallNetwork, executor.Config.InstallProxy = network, "cloudexec-registry-proxy:3128"
	if err := executor.InstallReady(context.Background()); err != nil {
		t.Skipf("install network not ready: %v", err)
	}
	return executor
}

func projectIn(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func runType(t *testing.T, executor *DockerExecutor, dir, jobType string, timeout time.Duration) RunResult {
	t.Helper()
	spec, err := jobspec.Resolve(jobType, jobspec.DefaultImages(), dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(context.Background(), RunRequest{Name: "cloudexec-i-" + jobType, FilesDir: dir, Spec: spec, Timeout: timeout})
	if err != nil {
		t.Fatalf("%s: %v (stderr: %s)", jobType, err, result.Stderr)
	}
	return result
}

func TestRealNpmInstallThenOfflineTest(t *testing.T) {
	executor := installExecutor(t)
	dir := projectIn(t, map[string]string{
		"package.json": `{"name":"app","version":"1.0.0","scripts":{"test":"node check.js"},"dependencies":{"left-pad":"1.3.0"}}`,
		"check.js":     `const pad = require('left-pad'); if (pad('7', 3, '0') !== '007') { console.error('wrong'); process.exit(1) } console.log('deps ok')`,
	})

	// Without installing, the offline test cannot find the package.
	before := runType(t, executor, dir, "npm-test", 2*time.Minute)
	if before.ExitCode == 0 {
		t.Fatal("the test passed without its dependency being installed")
	}

	install := runType(t, executor, dir, "npm-install", 5*time.Minute)
	if install.ExitCode != 0 || !exists(dir, "node_modules/left-pad/package.json") {
		t.Fatalf("install: exit %d\n%s\n%s", install.ExitCode, install.Stdout, install.Stderr)
	}

	after := runType(t, executor, dir, "npm-test", 2*time.Minute)
	if after.ExitCode != 0 || !strings.Contains(after.Stdout, "deps ok") {
		t.Fatalf("test after install: exit %d\n%s\n%s", after.ExitCode, after.Stdout, after.Stderr)
	}
}

func TestRealNpmInstallRunsNoProjectScripts(t *testing.T) {
	executor := installExecutor(t)
	script := `node -e "require('fs').writeFileSync('/workspace/%s','ran')"`
	dir := projectIn(t, map[string]string{
		"package.json": `{"name":"app","version":"1.0.0","dependencies":{"local-dep":"file:./local-dep"},"scripts":{"preinstall":"` + strings.ReplaceAll(strings.Replace(script, "%s", "root-preinstall", 1), `"`, `\"`) +
			`","install":"` + strings.ReplaceAll(strings.Replace(script, "%s", "root-install", 1), `"`, `\"`) +
			`","postinstall":"` + strings.ReplaceAll(strings.Replace(script, "%s", "root-postinstall", 1), `"`, `\"`) +
			`","prepare":"` + strings.ReplaceAll(strings.Replace(script, "%s", "root-prepare", 1), `"`, `\"`) + `"}}`,
		"local-dep/package.json": `{"name":"local-dep","version":"1.0.0","scripts":{"postinstall":"` + strings.ReplaceAll(strings.Replace(script, "%s", "dep-postinstall", 1), `"`, `\"`) + `"}}`,
		// A project .npmrc must not be able to turn scripts back on.
		".npmrc": "ignore-scripts=false\n",
	})
	result := runType(t, executor, dir, "npm-install", 5*time.Minute)
	if result.ExitCode != 0 {
		t.Fatalf("install: exit %d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
	for _, marker := range []string{"root-preinstall", "root-install", "root-postinstall", "root-prepare", "dep-postinstall"} {
		if exists(dir, marker) {
			t.Errorf("the %s script ran during installation", marker)
		}
	}
}

func TestRealInstallCannotReachAnythingButTheRegistries(t *testing.T) {
	executor := installExecutor(t)
	// A dependency pointing at an arbitrary server (or the cloud metadata address)
	// is how an install would be turned into an SSRF. The proxy must refuse it.
	for name, url := range map[string]string{
		"arbitrary https": "https://example.com/pkg.tgz",
		"metadata":        "https://169.254.169.254/latest/pkg.tgz",
		"github tarball":  "https://codeload.github.com/lodash/lodash/tar.gz/refs/heads/main",
	} {
		dir := projectIn(t, map[string]string{"package.json": `{"name":"app","version":"1.0.0","dependencies":{"bad":"` + url + `"}}`})
		result := runType(t, executor, dir, "npm-install", 3*time.Minute)
		if result.ExitCode == 0 || exists(dir, "node_modules/bad") {
			t.Errorf("%s: install of %s succeeded\n%s\n%s", name, url, result.Stdout, result.Stderr)
		}
	}
}

// The container is made by hand here (not from the allowlist) to probe the
// network itself, because project code is never allowed to run on this network.
func TestRealInstallNetworkHasNoWayOut(t *testing.T) {
	executor := installExecutor(t)
	dir := projectIn(t, map[string]string{"probe.js": `
const net = require('net'), dns = require('dns');
const fail = (m) => { console.error('PROBE FAILED: ' + m); process.exitCode = 1 };
const connect = (host, port) => new Promise((resolve) => {
  const s = net.connect({ host, port, timeout: 4000 });
  s.on('connect', () => { s.destroy(); resolve(true) });
  s.on('error', () => resolve(false));
  s.on('timeout', () => { s.destroy(); resolve(false) });
});
const viaProxy = (target) => new Promise((resolve) => {
  const s = net.connect({ host: 'cloudexec-registry-proxy', port: 3128, timeout: 15000 });
  let data = '';
  s.on('connect', () => s.write('CONNECT ' + target + ' HTTP/1.1\r\nHost: ' + target + '\r\n\r\n'));
  s.on('data', (d) => { data += d; s.destroy() });
  s.on('close', () => resolve(data.split('\r\n')[0]));
  s.on('error', () => resolve('error'));
  s.on('timeout', () => { s.destroy() });
});
(async () => {
  if (await connect('1.1.1.1', 443)) fail('direct internet access');
  if (await connect('8.8.8.8', 53)) fail('direct DNS server access');
  dns.lookup('example.com', (err) => { if (!err) fail('public DNS works') });
  const allowed = await viaProxy('registry.npmjs.org:443');
  if (!allowed.includes(' 200')) fail('registry refused: ' + allowed);
  for (const target of ['example.com:443', 'github.com:443', 'registry.npmjs.org:80', 'registry.npmjs.org:22', '169.254.169.254:443', '127.0.0.1:443']) {
    const answer = await viaProxy(target);
    if (answer.includes(' 200')) fail('proxy allowed ' + target);
  }
  setTimeout(() => console.log(process.exitCode ? 'PROBE FAILED' : 'probe ok'), 1500);
})();
`})
	spec := jobspec.CommandSpec{JobType: "probe", Runtime: "node", Image: jobspec.DefaultImages().Node, Argv: []string{"node", "probe.js"}, Install: true}
	result, err := executor.Run(context.Background(), RunRequest{Name: "cloudexec-i-probe", FilesDir: dir, Spec: spec, Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "probe ok") {
		t.Fatalf("exit %d\n%s\n%s", result.ExitCode, result.Stdout, result.Stderr)
	}
}

func TestRealPipInstallThenOfflineTest(t *testing.T) {
	executor := installExecutor(t)
	dir := projectIn(t, map[string]string{
		"requirements.txt":   "six==1.16.0\n",
		"tests/test_deps.py": "import six\n\n\ndef test_dependency_is_available():\n    assert six.PY3\n",
	})
	before := runType(t, executor, dir, "python-test", 2*time.Minute)
	if before.ExitCode == 0 {
		t.Fatal("the test passed without pytest being installed")
	}
	install := runType(t, executor, dir, "pip-install", 5*time.Minute)
	if install.ExitCode != 0 || !exists(dir, ".pydeps/six.py") || !exists(dir, ".pydeps/pytest") {
		t.Fatalf("install: exit %d\n%s\n%s", install.ExitCode, install.Stdout, install.Stderr)
	}
	after := runType(t, executor, dir, "python-test", 2*time.Minute)
	if after.ExitCode != 0 || !strings.Contains(after.Stdout, "1 passed") {
		t.Fatalf("test after install: exit %d\n%s\n%s", after.ExitCode, after.Stdout, after.Stderr)
	}
}

func TestRealGoDepsVendorThenOfflineTest(t *testing.T) {
	executor := installExecutor(t)
	dir := projectIn(t, map[string]string{
		"go.mod":       "module example\n\ngo 1.21\n\nrequire github.com/google/uuid v1.6.0\n",
		"main.go":      "package main\n\nimport \"github.com/google/uuid\"\n\nfunc main() { _ = uuid.New() }\n",
		"main_test.go": "package main\n\nimport (\n\t\"testing\"\n\n\t\"github.com/google/uuid\"\n)\n\nfunc TestUUID(t *testing.T) {\n\tif uuid.New() == uuid.Nil {\n\t\tt.Fatal(\"nil uuid\")\n\t}\n}\n",
	})
	before := runType(t, executor, dir, "go-test", 3*time.Minute)
	if before.ExitCode == 0 {
		t.Fatal("the test passed without its module being available")
	}
	install := runType(t, executor, dir, "go-deps", 5*time.Minute)
	if install.ExitCode != 0 || !exists(dir, "vendor/github.com/google/uuid") || !exists(dir, "go.sum") {
		t.Fatalf("install: exit %d\n%s\n%s", install.ExitCode, install.Stdout, install.Stderr)
	}
	after := runType(t, executor, dir, "go-test", 3*time.Minute)
	if after.ExitCode != 0 || !strings.Contains(after.Stdout, "ok") {
		t.Fatalf("test after install: exit %d\n%s\n%s", after.ExitCode, after.Stdout, after.Stderr)
	}
}
