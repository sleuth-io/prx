import re
import subprocess


def gather_context(diff: str) -> str:
    sections: list[str] = []

    dep_context = _analyze_dependency_changes(diff)
    if dep_context:
        sections.append(dep_context)

    code_context = _analyze_code_changes(diff)
    if code_context:
        sections.append(code_context)

    return (
        "\n\n".join(sections)
        if sections
        else "No additional codebase context gathered."
    )


def _analyze_dependency_changes(diff: str) -> str | None:
    dep_files = [
        "requirements.txt",
        "requirements",
        "pyproject.toml",
        "package.json",
        "Pipfile",
        "setup.py",
        "setup.cfg",
    ]
    changed_files = re.findall(r"^diff --git a/(.+?) b/", diff, re.MULTILINE)

    is_dep_change = any(
        any(dep_file in f for dep_file in dep_files) for f in changed_files
    )
    if not is_dep_change:
        return None

    added_packages = _extract_added_packages(diff)
    if not added_packages:
        return None

    results: list[str] = []
    for pkg in added_packages[:5]:
        usage = _grep_for_usage(pkg)
        results.append(f"Package '{pkg}': {usage}")

    return "## Dependency Analysis\n" + "\n".join(results)


def _extract_added_packages(diff: str) -> list[str]:
    packages: list[str] = []

    # Python requirements style: +package==version or +package>=version
    for match in re.finditer(r"^\+([a-zA-Z0-9_-]+)[=><!~]", diff, re.MULTILINE):
        pkg = match.group(1).lower()
        if pkg not in ("diff", "index"):
            packages.append(pkg)

    # package.json style: +"package": "version"
    for match in re.finditer(r'^\+"([a-zA-Z0-9@/_-]+)":\s*"', diff, re.MULTILINE):
        packages.append(match.group(1))

    return list(set(packages))


def _grep_for_usage(package: str) -> str:
    import_pattern = f"import {package}|from {package}|require.*{package}"
    try:
        result = subprocess.run(
            [
                "grep",
                "-r",
                "-l",
                "--include=*.py",
                "--include=*.js",
                "--include=*.ts",
                "--include=*.tsx",
                "-E",
                import_pattern,
                ".",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        files = [f for f in result.stdout.strip().split("\n") if f]
        if not files:
            return "not imported anywhere in the codebase"
        return f"imported in {len(files)} file(s): {', '.join(files[:5])}"
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return "could not determine usage"


def _analyze_code_changes(diff: str) -> str | None:
    changed_files = re.findall(r"^diff --git a/(.+?) b/", diff, re.MULTILINE)
    py_files = [f for f in changed_files if f.endswith(".py")]

    if not py_files:
        return None

    results: list[str] = []
    for filepath in py_files[:10]:
        module = _filepath_to_module(filepath)
        if module:
            importers = _grep_for_importers(module)
            results.append(f"'{filepath}' ({module}): {importers}")

    if not results:
        return None

    return "## Module Centrality\n" + "\n".join(results)


def _filepath_to_module(filepath: str) -> str | None:
    if not filepath.endswith(".py"):
        return None
    module = filepath.removesuffix(".py").replace("/", ".")
    # Strip common prefixes
    for prefix in ("src.", "lib."):
        if module.startswith(prefix):
            module = module[len(prefix) :]
    return module


def _grep_for_importers(module: str) -> str:
    parts = module.split(".")
    # Search for imports of the module or its parent
    patterns = [f"from {module}", f"import {module}"]
    if len(parts) > 1:
        parent = ".".join(parts[:-1])
        patterns.append(f"from {parent} import")

    combined_pattern = "|".join(patterns)
    try:
        result = subprocess.run(
            ["grep", "-r", "-l", "--include=*.py", "-E", combined_pattern, "."],
            capture_output=True,
            text=True,
            timeout=10,
        )
        files = [f for f in result.stdout.strip().split("\n") if f]
        if not files:
            return "no other modules import this"
        return f"imported by {len(files)} module(s)"
    except (subprocess.TimeoutExpired, subprocess.SubprocessError):
        return "could not determine importers"
