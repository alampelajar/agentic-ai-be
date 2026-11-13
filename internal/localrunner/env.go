package localrunner

import (
	"os"
	"strings"
)

// commandEnvAllowlist names the environment variables a project command keeps. A
// test or build script comes from the project, which may not be trusted, and it
// would otherwise see every variable of the person's session: cloud keys, tokens,
// LOCAL_RUNNER_TOKEN itself. These are only what compilers, package managers and
// the Docker CLI need to find their tools, caches and home folder.
var commandEnvAllowlist = map[string]bool{
	// Finding programs and the home and temporary folders.
	"PATH": true, "PATHEXT": true, "HOME": true, "USERPROFILE": true, "HOMEDRIVE": true, "HOMEPATH": true,
	"TEMP": true, "TMP": true, "TMPDIR": true, "USER": true, "USERNAME": true, "LOGNAME": true, "SHELL": true,
	"LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true, "TZ": true,
	// Windows needs these for almost any program to start.
	"SYSTEMROOT": true, "SYSTEMDRIVE": true, "WINDIR": true, "COMSPEC": true, "OS": true,
	"APPDATA": true, "LOCALAPPDATA": true, "PROGRAMDATA": true, "PROGRAMFILES": true, "PROGRAMFILES(X86)": true,
	"PROGRAMW6432": true, "COMMONPROGRAMFILES": true, "COMMONPROGRAMFILES(X86)": true,
	"NUMBER_OF_PROCESSORS": true, "PROCESSOR_ARCHITECTURE": true, "PROCESSOR_IDENTIFIER": true,
	// Toolchains and their caches.
	"GOROOT": true, "GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "GOTOOLCHAIN": true, "GOPROXY": true,
	"GOFLAGS": true, "GONOSUMDB": true, "GOPRIVATE": true,
	"NVM_DIR": true, "NVM_HOME": true, "NVM_SYMLINK": true, "NODE_PATH": true, "NPM_CONFIG_CACHE": true,
	"NPM_CONFIG_PREFIX": true, "COREPACK_HOME": true,
	"VIRTUAL_ENV": true, "CONDA_PREFIX": true, "PYENV_ROOT": true, "PYTHONHOME": true,
	"JAVA_HOME": true,
	// The Docker CLI, for the docker runtime.
	"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_CONFIG": true, "DOCKER_CERT_PATH": true, "DOCKER_TLS_VERIFY": true,
}

// commandEnv returns the allowed part of environ (KEY=value entries), plus the
// settings every project command runs with. Names are compared without regard to
// case, as Windows does.
func commandEnv(environ []string) []string {
	env := make([]string, 0, len(commandEnvAllowlist)+2)
	for _, entry := range environ {
		name, _, found := strings.Cut(entry, "=")
		if found && name != "" && commandEnvAllowlist[strings.ToUpper(name)] {
			env = append(env, entry)
		}
	}
	return append(env, "CI=true", "NO_COLOR=1")
}

// projectCommandEnv is commandEnv for this process's environment.
func projectCommandEnv() []string { return commandEnv(os.Environ()) }
