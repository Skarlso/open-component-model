package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"ocm.software/open-component-model/bindings/go/blob"
	"ocm.software/open-component-model/bindings/go/blob/compression"
	"ocm.software/open-component-model/bindings/go/cli/cmd/download/shared"
	ocmctx "ocm.software/open-component-model/bindings/go/cli/internal/context"
	descriptor "ocm.software/open-component-model/bindings/go/descriptor/runtime"
	ociaccess "ocm.software/open-component-model/bindings/go/oci/spec/access"
	ociaccessv1 "ocm.software/open-component-model/bindings/go/oci/spec/access/v1"
	ctfspec "ocm.software/open-component-model/bindings/go/oci/spec/repository/v1/ctf"
)

const (
	containerConfigPath = "/ocm/config"
	containerValuesPath = "/ocm/values"
)

func runExecutable(cmd *cobra.Command, data blob.ReadOnlyBlob, workDir string, run runSpec) error {
	logger := slog.Default()

	data, err := compression.Decompress(data)
	if err != nil {
		return fmt.Errorf("decompressing bootstrap resource failed: %w", err)
	}
	name := "bootstrap"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(workDir, name)
	if err := shared.SaveBlobToFile(data, executable); err != nil {
		return err
	}
	if err := os.Chmod(executable, 0o700); err != nil {
		return fmt.Errorf("making bootstrap resource executable failed: %w", err)
	}

	self, err := ocmctx.FromContext(cmd.Context()).Syscalls().Executable()
	if err != nil {
		return fmt.Errorf("locating the ocm binary failed: %w", err)
	}
	env := append(os.Environ(), run.env(run.configPath, run.values)...)
	env = append(env, EnvOCM+"="+self)

	// A link named ocm lets scripts call plain "ocm" and get this exact binary.
	binDir := filepath.Join(workDir, "bin")
	if err := linkSelf(self, binDir); err != nil {
		logger.Debug("ocm is not put on PATH, use "+EnvOCM, slog.String("error", err.Error()))
	} else {
		env = append(env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	c := exec.CommandContext(cmd.Context(), executable, run.args...)
	c.Env = env
	c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	return wrapExitError(c.Run())
}

func linkSelf(self, binDir string) error {
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		return err
	}
	name := "ocm"
	if goruntime.GOOS == "windows" {
		name += ".exe"
	}
	return os.Symlink(self, filepath.Join(binDir, name))
}

// runContainer hands the image reference to the container runtime, which pulls it with
// its own credentials. The image has to ship its own ocm binary.
func runContainer(cmd *cobra.Command, res *descriptor.Resource, run runSpec) error {
	image, err := imageReference(res)
	if err != nil {
		return err
	}
	containerRuntime, err := findContainerRuntime()
	if err != nil {
		return err
	}

	args := []string{"run", "--rm", "-i"}
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		args = append(args, "-t")
	}
	args = append(args, "-v", run.configPath+":"+containerConfigPath+":ro")
	values := ""
	if run.values != "" {
		values = containerValuesPath
		args = append(args, "-v", run.values+":"+values+":ro")
	}
	// The resolver points at the archive's host path, so mount it at that same path.
	if ctf, ok := run.repoSpec.(*ctfspec.Repository); ok {
		args = append(args, "-v", ctf.FilePath+":"+ctf.FilePath)
	}
	for _, e := range run.env(containerConfigPath, values) {
		args = append(args, "-e", e)
	}
	args = append(args, image)
	args = append(args, run.args...)

	slog.Debug("running bootstrap container", slog.String("runtime", containerRuntime), slog.Any("args", args))
	c := exec.CommandContext(cmd.Context(), containerRuntime, args...)
	c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	return wrapExitError(c.Run())
}

func imageReference(res *descriptor.Resource) (string, error) {
	var img ociaccessv1.OCIImage
	if err := ociaccess.Scheme.Convert(res.GetAccess(), &img); err != nil {
		return "", fmt.Errorf("bootstrap container %q must have an OCIImage access, local images are not supported yet: %w", res.Name, err)
	}
	return img.ImageReference, nil
}

func findContainerRuntime() (string, error) {
	for _, name := range []string{"docker", "podman"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errNoContainerRuntime
}

func wrapExitError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("bootstrapper exited with code %d", exitErr.ExitCode())
	}
	return err
}
