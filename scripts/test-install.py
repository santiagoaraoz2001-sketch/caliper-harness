#!/usr/bin/env python3
"""Installer contract checks using isolated homes and inert argv recorder bundles.

This exercises real shell lookup and launcher execution, not model inference.
Native library/runtime validation remains part of release qualification.
"""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

INSTALLER = Path(__file__).with_name("install.sh").resolve()


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="caliper-install-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.home = self.root / "User's Home With Spaces"
        self.home.mkdir()
        self.app = self.root / "Caliper's App With Spaces"
        self.app.mkdir()
        shutil.copy2(INSTALLER, self.app / "install.sh")
        (self.app / "lib").mkdir()
        (self.app / "runtime").mkdir()
        for file in ["lib/libkuzu.so", "lib/libkuzu.dylib", "runtime/manifest.json", "prompts.yaml"]:
            (self.app / file).write_text("fixture")
        (self.app / "caliper-harness").write_text(
            "#!/bin/sh\n" + 'printf "%s\\n" "$PWD" > "$CAPTURE"\n' +
            'printf "<%s>\\n" "$@" >> "$CAPTURE"\n' +
            'printf "%s\\n" "$PATH" "${LD_LIBRARY_PATH:-}" >> "$CAPTURE"\n' +
            'exit "${FIXTURE_EXIT:-0}"\n'
        )
        (self.app / "caliper-harness").chmod(0o755)
        self.project = self.root / "Project With Spaces"
        self.project.mkdir()
        self.capture = self.root / "argv.txt"
        self.env = dict(os.environ, HOME=str(self.home), SHELL="/bin/bash", PATH="/usr/bin:/bin", CAPTURE=str(self.capture))
        self.env.pop("ZDOTDIR", None)

    def install(self, *args, success=True):
        result = subprocess.run(["sh", str(self.app / "install.sh"), *args], env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stderr)
        return result

    def test_fresh_bash_reinstall_cwd_args_and_exit(self):
        self.install()
        self.install()
        profile = (self.home / ".profile").read_text()
        self.assertEqual(profile.count("# Caliper terminal command"), 1)
        result = subprocess.run(["bash", "--noprofile", "--norc", "-c", '. "$HOME/.profile"; command -v caliper; caliper run --prompt "a b" "$(printf c)"'], cwd=self.project, env=dict(self.env, FIXTURE_EXIT="7"), capture_output=True, text=True)
        self.assertEqual(result.returncode, 7, result.stderr)
        self.assertEqual(result.stdout.strip(), str(self.home / ".local/bin/caliper"))
        lines = self.capture.read_text().splitlines()
        self.assertEqual(lines[:5], [str(self.project.resolve()), "<run>", "<--prompt>", "<a b>", "<c>"])
        self.assertTrue(lines[5].startswith(str(self.app.resolve() / "bin") + ":"))

    def test_fresh_zsh_interactive_no_args(self):
        if not shutil.which("zsh"):
            self.skipTest("zsh unavailable")
        self.env["SHELL"] = "/bin/zsh"
        self.install()
        result = subprocess.run(["zsh", "-ic", 'command -v caliper; caliper'], cwd=self.project, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(self.home / ".local/bin/caliper"))
        self.assertEqual(self.capture.read_text().splitlines()[0], str(self.project.resolve()))
        self.assertNotIn("<tui>", self.capture.read_text())  # binary owns the no-argument TUI default

    def test_existing_bash_login_profile_and_custom_bin(self):
        (self.home / ".bash_profile").write_text("# keep this\n")
        target = self.home / "command bin"
        self.install("--bin-dir", str(target))
        self.assertTrue((self.home / ".bash_profile").read_text().startswith("# keep this\n"))
        self.assertFalse((self.home / ".profile").exists())
        result = subprocess.run(["bash", "--noprofile", "--norc", "-c", '. "$HOME/.bash_profile"; caliper version'], env=self.env, cwd=self.project)
        self.assertEqual(result.returncode, 0)

    def test_unrelated_command_preserved(self):
        target = self.home / ".local/bin"
        target.mkdir(parents=True)
        (target / "caliper").write_text("unrelated")
        self.install(success=False)
        self.assertEqual((target / "caliper").read_text(), "unrelated")
        self.assertFalse((self.home / ".profile").exists())

    def test_incomplete_bundle_refused(self):
        (self.app / "BUILD-INCOMPLETE").touch()
        self.install(success=False)
        self.assertFalse((self.home / ".local/bin/caliper").exists())

    def test_moved_bundle_reregister(self):
        self.install()
        new = self.root / "Moved App"
        self.app.rename(new)
        old = self.home / ".local/bin/caliper"
        result = subprocess.run([str(old)], env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("moved or is missing", result.stderr)
        self.app = new
        self.install()
        result = subprocess.run([str(old), "version"], env=self.env, cwd=self.project)
        self.assertEqual(result.returncode, 0)

    def test_linux_library_path_branch(self):
        platform_bin = self.root / "platform-bin"
        platform_bin.mkdir()
        (platform_bin / "uname").write_text("#!/bin/sh\necho Linux\n")
        (platform_bin / "uname").chmod(0o755)
        self.env["PATH"] = str(platform_bin) + ":" + self.env["PATH"]
        self.env["LD_LIBRARY_PATH"] = "/prior/library"
        (self.app / "runtime/manifest.json").unlink()
        (self.app / "lib/libkuzu.dylib").unlink()
        self.install()
        result = subprocess.run([str(self.home / ".local/bin/caliper"), "version"], env=self.env, cwd=self.project)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.capture.read_text().splitlines()[-1], str(self.app.resolve() / "lib") + ":/prior/library")

    def test_unset_shell_uses_profile(self):
        del self.env["SHELL"]
        self.install()
        self.assertIn("# Caliper terminal command", (self.home / ".profile").read_text())

    def test_no_path_leaves_profiles_untouched(self):
        self.env["SHELL"] = "/bin/fish"
        self.install("--no-path")
        self.assertFalse((self.home / ".profile").exists())
        self.assertTrue((self.home / ".local/bin/caliper").is_file())


if __name__ == "__main__":
    unittest.main(verbosity=2)
