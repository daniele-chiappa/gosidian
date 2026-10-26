# The IN folder

A project's **IN folder**, `<project>/IN/`, holds files to work on, under
their own names and in their own tree: a quarter of invoices, a folder of
scans, the documents of a RAG project. They are files, not notes: the
[pipelines](pipelines.md#the-in-folder) take them in, one run per file or
per batch, and write their results as notes (IMP-172, ADR-037). A markdown
file becomes a note through a pipeline too, with `in.import`
([documents from the IN folder](pipelines.md#documents-from-the-in-folder)).

## Turning it on

The project flag `use_in` gives a project its IN folder. Turning
`use_pipelines` on turns it on too; turning the pipelines off leaves it,
with what it holds. The Projects window has the chip **IN**, and
`PUT /api/v1/projects/<name>` takes `{"use_in": true}`.

- The folder is made when the flag goes on, and again at start when it is
  missing.
- A folder named `IN` that already holds notes keeps the flag off (409):
  they would leave the index. Move them out first.
- Turned off, the folder stays where it is and is a folder like any other
  again: its `.md` files are notes.
- A project whose pipelines were on before 2.81 has no IN folder yet:
  its chip turns it on.

The IN folder works with the pipelines off on the server too: its window
lists what it holds, and files can arrive, before a pipeline takes them.

## What is in it

Files, under any name and of any kind, in folders. Nothing in the IN
folder is a note, a `.md` file included:

- the scan, the search, the graph, the tags and the note count leave it
  out; the tree shows it as one row, with what it holds, not its files;
- the note tools and the note API refuse its paths (a note cannot be
  created, moved or renamed into it, an attachment cannot be stored in it);
- the zips of the vault and of the project leave it out, as it can weigh
  gigabytes; the zip of the IN folder, or of a folder in it, from the tree's
  menu or the IN window, has all of it;
- a link (a symlink, in it or on the way to it) is not there: not listed,
  not served, not moved into; hidden files and folders are left out too.

## How files arrive

Files arrive on the server's disk, in `<vault>/<project>/IN/`, by any way
that writes there: `rsync` or `scp` to the server, a sync client, a folder
of the host or a volume mounted at that path in the compose file.

```yaml
services:
  gosidian:
    volumes:
      - ./vault:/vault
      # The IN folder of the project archive on a disk of its own.
      - /mnt/scans:/vault/archive/IN
```

Every 20 seconds the server scans the IN folders: a file there for the
first time, or changed (another size or modification time), is **new**;
a file gone is forgotten. It hashes what arrived (SHA-256) and groups the
files by **batch**: those that arrived in the same first-level folder of
the IN folder (the files at its root form one batch of their own).

A batch **closes** when nothing changed in its folder for 60 seconds, or
at once when a file named `.done` appears in it (a marker that a copy
script writes last; the server removes it when it can, and a marker it
cannot remove counts once). A closed batch is what the `in` pipelines take.

Files also arrive from a remote store through a plugin's source (below),
and from the web UI and the REST API, uploaded (next).

## Uploading

Who writes in the project uploads files into the folder the IN window
shows (IMP-172 phase 5):

- **Upload files**, **Upload folder** (with its subfolders, under its own
  name) and **Upload zip**, or files and folders dropped on the window;
- a zip chosen with **Upload zip** is unpacked on the server: into its own
  folder when all its files are in one (a zip of a folder), else into a
  folder named after it. A zip uploaded as a file, or dropped, stays a zip;
- hidden files and folders (`.DS_Store`, `.git/`) are left out, and so are
  names IN does not take (a `\`, a control character, spaces around a
  name); the window says how many;
- for many small files a zip goes better: it arrives in a few pieces, and
  the window says so.

The uploads go on in the background, three files at a time, whatever
window is open: the top bar shows how far they are, and the **Uploads**
window has each one with its speed, the time left, pause, resume, cancel,
and its files, each with a retry. A file arrives in pieces of 1 to 32 MiB,
which follow the line's speed. A piece the network lost is sent again from
the bytes the server has; a network error waits and tries again; a server
whose disk is nearly full pauses the upload. Closing the tab while an
upload is on its way asks first.

**A file whose place is taken**: the upload asks once what to do with all
of them: **Replace** (the old file goes to the trash, or for good without
it; a folder is never replaced by a file), **Keep both** (the new one as
`name (2).ext`) or **Skip**. A file that is the same as the one there
(same size and SHA-256) is left as it is, whatever the choice: uploading
a folder again sends only what changed.

**After a reload**, or in another browser, the browser no longer has the
files, but the server has the bytes that arrived: the IN window shows the
upload cut short, and **Resume** asks for the same files (or folder, or
zips) again, then goes on from there. The same files opened again into
the same folder resume too.

When every file has arrived, the server places them in the background:
each with a rename (the zips unpacked first, see the
[caps](configuration.md#uploads-into-in)), their rows written with their
hash, and the upload is **one batch** of each first-level folder it fills,
closed at once: the `in` pipelines take it without waiting for the quiet
time. An upload with no piece for 24 hours (`[in] session_ttl`) goes, with
what arrived.

The free disk the server keeps (5 GiB by default) is the only limit out of
the box; the installer sets the others, by way in
([configuration](configuration.md#uploads-into-in)).

### With curl

The same protocol, for a script (a session token from `POST /api/v1/login`
in `$T`):

```sh
F=big.pdf; S=$(stat -c %s "$F")
ID=$(curl -s -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"dir\":\"scans\",\"files\":[{\"path\":\"$F\",\"size\":$S}]}" \
  https://notes.example.com/api/v1/in/archive/uploads | jq -r .id)
# 32 MiB pieces, each from the bytes the server has: a piece cut short
# keeps what arrived, and the next goes on from there.
OFF=0
while [ "$OFF" -lt "$S" ]; do
  tail -c +$((OFF + 1)) "$F" | head -c 33554432 |
    curl -s -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/octet-stream' --data-binary @- \
      "https://notes.example.com/api/v1/in/archive/uploads/$ID/files/0?offset=$OFF" > /dev/null
  OFF=$(curl -s -H "Authorization: Bearer $T" https://notes.example.com/api/v1/in/archive/uploads/$ID | jq '.files[0].received')
done
curl -s -X POST -H "Authorization: Bearer $T" https://notes.example.com/api/v1/in/archive/uploads/$ID/complete
```

### From an agent (MCP)

An agent uploads with `memory_in_upload`, the same protocol without a
session of the web UI ([upload flow](mcp/upload.md#files-for-the-pipelines-memory_in_upload)):
`open` lists the files and answers with a **ticket**, which the agent's
shell sends the pieces with, as its bearer, to `<mcp>/in-upload/<id>`;
then `complete`. The upload is the token's account's, so its IN window
shows it too, and `[in.mcp]` caps it.

## Sources

A plugin may offer **sources**: remote stores it reads, such as a
Nextcloud folder, an S3 bucket or a Drive folder, set up in the plugin
with their credentials ([plugins](plugins.md#sources)). The owner binds
one to a project, and it shows as a folder at the root of the IN folder:

```bash
gosidian plugin bind storage fatture archive --as nextcloud
```

`archive/IN/nextcloud/` then holds the files of the source, with their
folders, as the local files are held: in `in.db`, in batches, taken by the
`in` pipelines, handed to the plugins by reference, listed by the IN
window and `in.list`. The bytes stay in the source: gosidian reads a file
from there when a node, a plugin or a download asks for it, a range at a
time, with nothing on its disk.

- **Checks**: every minute (`--every`, at least 20 seconds), and at once
  from the IN window's **Check now** or `POST /api/v1/in/<project>/scan`.
  A source that gives its changes is read from the last check's cursor,
  and listed in full once an hour; another is listed in full each time.
- **Versions**: a file is new when its content changed, by its SHA-256
  when the source gives one, else by its etag. A file of a source has no
  SHA-256 of gosidian's: it is not downloaded to hash it.
- **Sizes**: a file whose size the source knows only when it is read (a
  native Google document, exported) shows no size (—, `size: -1` in the
  API and in `trigger.size`) and counts no bytes; read, it is read whole,
  ranges too.
- **Batches**: by the first folder inside the source
  (`nextcloud/2024/…`), or the source's root; a quiet minute or a `.done`
  marker closes one, as on the disk. The marker stays in the source
  (gosidian deletes nothing there) and counts once.
- **What it allows**: statuses, as any file. A file of a source is never
  deleted from gosidian, and never moves out of its source; it moves
  inside it only when the binding has `--write` and the source can move
  files. A target outside the source is read inside it: `done/` is
  `nextcloud/done/`, so a pipeline that moves its files to `done/` works on
  the disk's and on a source's alike. A file of the disk does not move
  into a source. A folder of a source is not zipped.
- **Bindings**: a folder of the IN folder that holds local files is not
  bound over. A source may be bound to several projects. Unbound, its
  folder is forgotten at the next check; the source keeps its files.
- **The pipelines off on the server**: the plugins are not there, so the
  sources are not checked nor read; what a source's folder held stays, and
  the IN window says why.

## The status of a file

| Status | |
|---|---|
| `new` | arrived or changed, and no run took it yet |
| `working` | a run took it |
| `done` | the run that took it ended well, or the pipeline said so (`in.move`) |
| `error` | the run that took it failed, or the pipeline said so |
| `ignored` | set aside, by a pipeline or by hand |

A file that changes is new again. The state of the files, with their hash
and the run that took them, lives in `<state-dir>/in.db` (SQLite, outside
the vault and its git history), not in the vault.

A file has one status, and one run: when two `in` pipelines take it, the
one that ends last sets it. A pipeline that reads the files only (a
summary of each batch, a count, a notice) says `take: false` in its
trigger: it does not take them, and leaves their status to the pipeline
that works on them.

A project deleted forgets the state of its IN folder and the sources bound
to it, so a later project of the name starts afresh. Restored from the
trash, its files are new again; a pipeline per file does not run twice on
the same content, as a run is keyed by the file and its content.

## The IN window

A click on the IN row of the tree opens the **IN window** of the project:
the folder a level at a time, its subfolders with their files, size and new
files, then its files with their size, date, status and the run that took
them. A search, or a status, looks under the folder. Who writes in the
project chooses files and:

- moves them to a folder of the IN folder (`done/`);
- sets them aside, or back as new;
- deletes them, into the trash (a folder needs the trash, a file without it
  goes for good, as the confirmation says);
- uploads files, a folder or zips into the folder shown, or those dropped
  on the window ([Uploading](#uploading));
- starts on them a pipeline of the project, by hand, the way its trigger
  would: an `in` pipeline per file once per file, an `in` pipeline per batch
  once with the files, both with the files their trigger takes only (the
  others are skipped, and the window says why); any other pipeline once
  with all the files, its trigger shaped as a batch's.

A file downloads from `/vault-files/` (a download, but for images), a
folder as a zip. The window follows the folder live.

## Git

The managed block of the vault's `.gitignore` leaves each IN folder out,
and the uploads on their way (`IN/.uploads/`) of a tracked one.
A project that wants its IN folder in the history sets `track_in_git`
(chip **IN git**, asked first): git then tracks it, but its files over
50 MiB, which a commit takes out again and the `.gitignore` names from then
on. Turning it off takes the folder out of git's index at the next commit;
the files stay on disk, the history keeps what was pushed.

## The API

| Route | |
|---|---|
| `GET /api/v1/in/<project>?dir=&recursive=&match=&kinds=&status=&limit=&cursor=` | counts, the subfolders of `dir`, a page of its files; with `recursive=true` every file under it that the filters select; the sources bound, with their state |
| `POST /api/v1/in/<project>/move` `{from, to}` | a file or a folder to another place of the IN folder (`to` ending in `/` is a folder to move it into) |
| `POST /api/v1/in/<project>/status` `{paths, status}` | sets the status of files |
| `POST /api/v1/in/<project>/delete` `{paths}` | into the trash |
| `POST /api/v1/in/<project>/run` `{pipeline, paths}` | a pipeline of the project on files, by hand, as its trigger would: `{runs, skipped}` |
| `POST /api/v1/in/<project>/scan` `{folder}` | a check now: of a source's folder, or of all of them and the disk |
| `GET /api/v1/in/<project>/uploads` | the account's uploads into the IN folder |
| `POST /api/v1/in/<project>/uploads` `{dir, policy, files: [{path, size, unzip, modified}]}` | opens an upload (the same files again: the same upload), with whether each file's place is taken |
| `PUT /api/v1/in/<project>/uploads/<id>/files/<n>?offset=X` | a piece of a file, at most 64 MiB, from at most the bytes received (those the server has are skipped; past them, 409 says how many) |
| `POST /api/v1/in/<project>/uploads/<id>/complete` `{policy, drop}` | places the files in the background; `GET …/uploads/<id>` follows it to its results |
| `DELETE /api/v1/in/<project>/uploads/<id>` | drops an upload with what arrived |
| `GET /vault-files/<project>/IN/<path>` | a file, with ranges |

Paths are vault paths, or relative to the IN folder. Reading needs read
access to the project, the rest write access; an upload is its opener's. Reference:
`internal/api/v1/spec/openapi.yaml`, tag `in`.
