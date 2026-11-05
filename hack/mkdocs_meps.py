# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
"""Publish MEPs without copying or modifying their repository sources."""

import os
import posixpath
from pathlib import Path
from urllib.parse import quote, unquote, urlsplit, urlunsplit

from markdown.extensions import Extension
from markdown.treeprocessors import Treeprocessor
from mkdocs.exceptions import PluginError
from mkdocs.structure.files import File, InclusionLevel


class ProposalLinks(Treeprocessor):
    def __init__(self, md, extension):
        super().__init__(md)
        self.extension = extension

    def run(self, root):
        page = self.extension.page
        if page is None:
            return
        for element in root.iter():
            attribute = {"a": "href", "img": "src"}.get(element.tag)
            if attribute and element.get(attribute):
                element.set(attribute, self.extension.rewrite(
                    element.get(attribute), image=element.tag == "img"))


class ProposalExtension(Extension):
    def __init__(self, config):
        super().__init__()
        self.config = config
        self.root = Path(__file__).resolve().parent.parent
        self.page = None

    def extendMarkdown(self, md):
        # Inline/reference links have been parsed, but MkDocs has not yet
        # resolved them against its virtual documentation tree.
        md.treeprocessors.register(ProposalLinks(md, self), "mep_links", 1)

    def rewrite(self, url, *, image=False):
        parsed = urlsplit(url)
        if parsed.scheme or parsed.netloc or not parsed.path or parsed.path.startswith("/"):
            return url
        target = (Path(self.page.file.abs_src_path).parent / unquote(parsed.path)).resolve()
        if not target.is_relative_to(self.root):
            raise PluginError(f"MEP link escapes the repository: {url}")
        for source, prefix in [(self.root / "enhancements/meps", "meps"),
                               (self.root / "docs", "")]:
            if target.is_relative_to(source):
                if target.is_dir() and (target / "README.md").is_file():
                    target = target / "README.md"
                virtual = posixpath.join(prefix, target.relative_to(source).as_posix())
                relative = posixpath.relpath(virtual, posixpath.dirname(self.page.file.src_uri))
                return urlunsplit(("", "", quote(relative, safe="/"), parsed.query, parsed.fragment))
        if image:
            raise PluginError(f"MEP images must be under docs/ or enhancements/meps/: {url}")
        if not target.exists():
            raise PluginError(f"MEP repository link does not exist: {url}")
        kind = "tree" if target.is_dir() else "blob"
        path = quote(target.relative_to(self.root).as_posix(), safe="/")
        return f"{self.config.repo_url.rstrip('/')}/{kind}/main/{path}" + (
            f"?{parsed.query}" if parsed.query else "") + (f"#{parsed.fragment}" if parsed.fragment else "")


def on_config(config):
    config.markdown_extensions = [extension for extension in config.markdown_extensions
                                  if not isinstance(extension, ProposalExtension)]
    config.markdown_extensions.append(ProposalExtension(config))
    return config


def on_files(files, config):
    root = Path(__file__).resolve().parent.parent
    proposals = root / "enhancements/meps"
    # Reserve the virtual namespace, including empty directories and dangling
    # symlinks, rather than silently hiding files from a previous wrapper run.
    if os.path.lexists(Path(config.docs_dir) / "meps"):
        raise PluginError("docs/meps is reserved for virtual MEP pages; move or remove the existing path")
    if proposals.is_symlink() or not proposals.is_dir():
        raise PluginError("enhancements/meps must be a real directory")
    outputs = {file.dest_uri for file in files}
    sources = {file.src_uri for file in files}
    navigation = [{"Overview": "contributing/enhancements.md"}]
    for source in sorted(proposals.rglob("*")):
        if source.is_symlink():
            raise PluginError(f"MEP sources must not be symlinks: {source.relative_to(root)}")
        if not source.is_file():
            continue
        relative = source.relative_to(proposals)
        uri = f"meps/{relative.as_posix()}"
        file = File.generated(config, uri, abs_src_path=str(source), inclusion=InclusionLevel.INCLUDED)
        if uri in sources or any(file.dest_uri == other or file.dest_uri.startswith(other + "/")
                                 or other.startswith(file.dest_uri + "/") for other in outputs):
            raise PluginError(f"MEP documentation path collision: {uri} -> {file.dest_uri}")
        files.append(file)
        sources.add(uri)
        outputs.add(file.dest_uri)
        if len(relative.parts) == 2 and relative.name == "README.md":
            number = relative.parts[0].split("-", 1)[0]
            navigation.append({f"MEP-{number}": uri})
    # on_files runs on every serve rebuild. Replace rather than append entries
    # so added and deleted proposal directories update the navigation too.
    def update(items):
        for item in items:
            if not isinstance(item, dict):
                continue
            for title, value in item.items():
                if title == "Enhancement Proposals":
                    item[title] = navigation
                elif isinstance(value, list):
                    update(value)
    update(config.nav)
    return files


def on_page_markdown(markdown, page, config, files):
    for extension in config.markdown_extensions:
        if isinstance(extension, ProposalExtension):
            extension.page = page if page.file.src_uri.startswith("meps/") else None
    if page.file.src_uri.startswith("meps/"):
        source = "enhancements/" + page.file.src_uri
        page.edit_url = f"{config.repo_url.rstrip('/')}/edit/main/{quote(source, safe='/')}"
    return markdown
