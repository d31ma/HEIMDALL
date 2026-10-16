#!/usr/bin/env python3
"""Describe relocated Go tests without editing the production source tree."""
from pathlib import Path
import json
import re
import sys


def main():
    root = Path(sys.argv[1]).resolve()
    module = root / 'app/server'
    tests = root / 'tests/server'
    assert module.is_dir() and tests.is_dir(), 'Expected sibling app and tests source directories'
    replacements = {}
    for source in sorted(tests.rglob('*.go')):
        assert source.name.endswith('_test.go'), 'Production Go files do not belong in tests/server'
        assert not source.is_symlink(), 'Test-source symlinks are not supported'
        destination = module / source.relative_to(tests)
        assert not destination.exists(), 'Test source must remain outside app/server'
        production = sorted(destination.parent.glob('*.go'))
        packages = set()
        for path in production:
            match = re.search(r'^package\s+(\w+)\b', path.read_text(), re.M)
            if match:
                packages.add(match.group(1))
        test_package = re.search(r'^package\s+(\w+)\b', source.read_text(), re.M)
        assert len(packages) == 1 and test_package, 'Test needs an unambiguous native package'
        package = next(iter(packages))
        assert test_package.group(1) in {package, package + '_test'}, 'Test package differs from its native package'
        replacements[str(destination)] = str(source)
    assert replacements, 'No relocated native Go tests found'
    print(json.dumps({'Replace': replacements}, sort_keys=True))


if __name__ == '__main__':
    main()
