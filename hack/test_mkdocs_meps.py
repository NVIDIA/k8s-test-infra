# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
"""CPU-only regressions for the real MkDocs MEP hook and rendered site."""

import hashlib
import shutil
import tempfile
import unittest
from pathlib import Path

from mkdocs.commands.build import build
from mkdocs.config import load_config
from mkdocs.exceptions import Abort


class ProposalBuildTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.write("hack/mkdocs_meps.py", Path(__file__).with_name("mkdocs_meps.py").read_text())
        self.write("docs/index.md", "# Home\n")
        self.write("docs/contributing/enhancements.md", "# Proposals\n")
        self.write("docs/guide.md", "# Guide\n\n## Details\n")
        self.write("enhancements/meps/0001-first/README.md", "# First\n")
        self.write("mkdocs.yml", """site_name: Test
repo_url: https://github.com/example/project
edit_uri: edit/main/docs/
strict: true
hooks: [hack/mkdocs_meps.py]
watch: [enhancements/meps]
exclude_docs: |
  plans/
  superpowers/
nav:
  - Home: index.md
  - Guide: guide.md
  - Enhancement Proposals: contributing/enhancements.md
""")
        self.config = load_config(str(self.root / "mkdocs.yml"))

    def write(self, path, text):
        destination = self.root / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(text)
        return destination

    def proposal(self, text):
        return self.write("enhancements/meps/0001-first/README.md", "# First\n\n" + text)

    def html(self, path="meps/0001-first/index.html"):
        return (self.root / "site" / path).read_text()

    def sources(self):
        return {path.relative_to(self.root): hashlib.sha256(path.read_bytes()).hexdigest()
                for directory in ["docs", "enhancements"]
                for path in (self.root / directory).rglob("*") if path.is_file()}

    def test_links_references_and_edit_url(self):
        self.write("internal/source.go", "package source\n")
        self.proposal("[guide](../../../docs/guide.md#details)\n\n"
                      "[source][ref]\n\n[ref]: ../../../internal/source.go?plain=1#L1\n\n"
                      "[directory](../../../internal)\n\n"
                      "`[literal](../../../missing.go)`\n")
        before = self.sources()
        build(self.config)
        html = self.html()
        self.assertIn('href="../../guide/#details"', html)
        self.assertIn('href="https://github.com/example/project/blob/main/internal/source.go?plain=1#L1"', html)
        self.assertIn('href="https://github.com/example/project/tree/main/internal"', html)
        self.assertIn('href="https://github.com/example/project/edit/main/enhancements/meps/0001-first/README.md"', html)
        self.assertIn('[literal](../../../missing.go)', html)
        self.assertEqual(before, self.sources())
        self.assertFalse((self.root / "docs/meps").exists())

    def test_dynamic_navigation_add_delete_and_asset_edit(self):
        self.write("enhancements/meps/0001-first/image.svg", "first")
        self.proposal("![Image](image.svg)")
        build(self.config)
        self.assertEqual((self.root / "site/meps/0001-first/image.svg").read_text(), "first")
        self.write("enhancements/meps/0001-first/image.svg", "second")
        self.write("enhancements/meps/0006-added/README.md", "# Added\n")
        build(self.config)
        self.assertEqual((self.root / "site/meps/0001-first/image.svg").read_text(), "second")
        self.assertIn("MEP-0006", self.html())
        shutil.rmtree(self.root / "enhancements/meps/0006-added")
        build(self.config)
        self.assertNotIn("MEP-0006", self.html())
        self.assertFalse((self.root / "site/meps/0006-added").exists())
        self.assertEqual(sum(type(value).__name__ == "ProposalExtension"
                             for value in self.config.markdown_extensions), 1)

    def test_proposal_and_docs_images(self):
        self.write("docs/img/picture.svg", "docs image")
        self.write("enhancements/meps/0001-first/image.svg", "proposal image")
        self.proposal("![local](image.svg)\n\n![docs](../../../docs/img/picture.svg)")
        build(self.config)
        self.assertIn('src="image.svg"', self.html())
        self.assertIn('src="../../img/picture.svg"', self.html())
        self.assertEqual((self.root / "site/img/picture.svg").read_text(), "docs image")

    def test_cross_proposal_directory_links(self):
        self.write("enhancements/meps/0002-other/README.md", "# Other\n")
        self.proposal("[Other](../0002-other/)")
        build(self.config)
        self.assertIn('href="../0002-other/"', self.html())

    def test_docs_links_to_proposals(self):
        self.write("enhancements/meps/0001-first/image.svg", "proposal image")
        self.write("docs/guide.md", "# Guide\n\n"
                   "[First](../enhancements/meps/0001-first/README.md#first)\n\n"
                   "[Directory](../enhancements/meps/0001-first/)\n\n"
                   "![Image](../enhancements/meps/0001-first/image.svg)\n")
        before = self.sources()
        build(self.config)
        html = self.html("guide/index.html")
        self.assertIn('href="../meps/0001-first/#first"', html)
        self.assertIn('href="../meps/0001-first/"', html)
        self.assertIn('src="../meps/0001-first/image.svg"', html)
        self.assertEqual(before, self.sources())

    def test_docs_links_to_proposals_from_copied_tree(self):
        self.write("docs/contributing/enhancements.md", "# Proposals\n\n"
                   "[First](../../enhancements/meps/0001-first/README.md#first)\n")
        with tempfile.TemporaryDirectory() as temporary:
            copied_docs = Path(temporary) / "docs"
            shutil.copytree(self.root / "docs", copied_docs)
            self.config.docs_dir = str(copied_docs)
            build(self.config)
        self.assertIn('href="../../meps/0001-first/#first"',
                      self.html("contributing/enhancements/index.html"))

    def test_broken_non_proposal_docs_links_still_fail(self):
        self.write("docs/guide.md", "# Guide\n\n[Missing](missing.md)\n")
        with self.assertRaises(Abort):
            build(self.config)

    def test_broken_proposal_docs_links_fail(self):
        self.write("docs/guide.md", "# Guide\n\n"
                   "[Missing](../enhancements/meps/0001-first/missing.md)\n")
        with self.assertRaises(Abort):
            build(self.config)

    def test_outside_images_fail_explicitly(self):
        self.write("internal/image.svg", "outside image")
        self.proposal("![bad](../../../internal/image.svg)")
        with self.assertRaisesRegex(Abort, "Aborted"):
            build(self.config)

    def test_missing_repository_links_fail(self):
        self.proposal("[bad](../../../internal/missing.go)")
        with self.assertRaises(Abort):
            build(self.config)

    def test_repository_escape_fails(self):
        self.proposal("[bad](../../../../outside.txt)")
        with self.assertRaises(Abort):
            build(self.config)

    def test_reserved_namespace_preserves_existing_content(self):
        existing = self.write("docs/meps/user.md", "keep me")
        with self.assertRaises(Abort):
            build(self.config)
        self.assertEqual(existing.read_text(), "keep me")

    def test_dangling_namespace_symlink_fails(self):
        (self.root / "docs/meps").symlink_to(self.root / "missing")
        with self.assertRaises(Abort):
            build(self.config)
        self.assertTrue((self.root / "docs/meps").is_symlink())

    def test_source_symlinks_fail(self):
        (self.root / "enhancements/meps/dangling.md").symlink_to(self.root / "missing")
        with self.assertRaises(Abort):
            build(self.config)
        self.assertTrue((self.root / "enhancements/meps/dangling.md").is_symlink())

    def test_output_collision_fails(self):
        self.write("enhancements/meps/0001-first/index.md", "# Duplicate\n")
        with self.assertRaises(Abort):
            build(self.config)

    def test_excluded_docs_are_absent(self):
        for directory in ["plans", "superpowers"]:
            self.write(f"docs/{directory}/private.md", "# Secret\n\nPRIVATE_CANARY\n")
        before = self.sources()
        build(self.config)
        self.assertFalse(any("PRIVATE_CANARY" in path.read_text()
                             for path in (self.root / "site").rglob("*.html")))
        self.assertEqual(before, self.sources())


if __name__ == "__main__":
    unittest.main()
