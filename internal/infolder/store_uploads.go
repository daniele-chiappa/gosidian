package infolder

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// uploadSchema holds the uploads on their way (IMP-172 phase 5): a row per
// upload, a row per file with the bytes received and the state of its
// SHA-256 at that point, so that an upload resumes after a restart too.
const uploadSchema = `
CREATE TABLE IF NOT EXISTS uploads (
	id          TEXT PRIMARY KEY,
	project     TEXT NOT NULL,
	dir         TEXT NOT NULL,
	policy      TEXT NOT NULL,
	owner       TEXT NOT NULL,
	owner_id    TEXT NOT NULL,
	channel     TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	state       TEXT NOT NULL DEFAULT 'open',
	created     INTEGER NOT NULL,
	updated     INTEGER NOT NULL,
	error       TEXT NOT NULL DEFAULT '',
	batches     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS uploads_project ON uploads(project, owner_id);
CREATE TABLE IF NOT EXISTS upload_files (
	upload   TEXT NOT NULL,
	n        INTEGER NOT NULL,
	path     TEXT NOT NULL,
	size     INTEGER NOT NULL,
	unzip    INTEGER NOT NULL DEFAULT 0,
	modified INTEGER NOT NULL DEFAULT 0,
	received INTEGER NOT NULL DEFAULT 0,
	hash     BLOB,
	sha256   TEXT NOT NULL DEFAULT '',
	dropped  INTEGER NOT NULL DEFAULT 0,
	result   TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (upload, n)
);
`

const uploadCols = `id, project, dir, policy, owner, owner_id, channel, fingerprint, state, created, updated, error, batches`

func scanUpload(r scanner) (Upload, error) {
	var u Upload
	var created, updated int64
	var batches string
	if err := r.Scan(&u.ID, &u.Project, &u.Dir, &u.Policy, &u.Owner, &u.OwnerID, &u.Channel, &u.fingerprint,
		&u.State, &created, &updated, &u.Error, &batches); err != nil {
		return Upload{}, err
	}
	u.Created, u.Updated = fromNanos(created), fromNanos(updated)
	u.batches = map[string]string{}
	if batches != "" {
		_ = json.Unmarshal([]byte(batches), &u.batches)
	}
	return u, nil
}

const uploadFileCols = `n, path, size, unzip, modified, received, sha256, dropped, result`

func scanUploadFile(r scanner) (UploadFile, error) {
	var f UploadFile
	var result string
	if err := r.Scan(&f.N, &f.Path, &f.Size, &f.Unzip, &f.Modified, &f.Received, &f.SHA256, &f.Dropped, &result); err != nil {
		return UploadFile{}, err
	}
	if result != "" {
		var res FileResult
		if json.Unmarshal([]byte(result), &res) == nil {
			f.Result = &res
		}
	}
	return f, nil
}

// insertUpload records a new upload and its files.
func (s *Store) insertUpload(u Upload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO uploads (`+uploadCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		u.ID, u.Project, u.Dir, u.Policy, u.Owner, u.OwnerID, u.Channel, u.fingerprint, u.State,
		nanos(u.Created), nanos(u.Updated)); err != nil {
		return err
	}
	st, err := tx.Prepare(`INSERT INTO upload_files (upload, n, path, size, unzip, modified) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, f := range u.Files {
		if _, err := st.Exec(u.ID, f.N, f.Path, f.Size, f.Unzip, f.Modified); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upload returns an upload, with its files when files is set.
func (s *Store) upload(id string, files bool) (Upload, error) {
	u, err := scanUpload(s.db.QueryRow(`SELECT `+uploadCols+` FROM uploads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Upload{}, ErrUploadNotFound
	}
	if err != nil {
		return Upload{}, err
	}
	rows, err := s.db.Query(`SELECT n, size, received FROM upload_files WHERE upload = ?`, id)
	if err != nil {
		return Upload{}, err
	}
	for rows.Next() {
		var n int
		var size, received int64
		if err := rows.Scan(&n, &size, &received); err != nil {
			rows.Close()
			return Upload{}, err
		}
		u.FileCount++
		u.Bytes += size
		u.Received += received
	}
	rows.Close()
	if !files {
		return u, nil
	}
	rows, err = s.db.Query(`SELECT `+uploadFileCols+` FROM upload_files WHERE upload = ? ORDER BY n`, id)
	if err != nil {
		return Upload{}, err
	}
	defer rows.Close()
	u.Files = []UploadFile{}
	for rows.Next() {
		f, err := scanUploadFile(rows)
		if err != nil {
			return Upload{}, err
		}
		u.Files = append(u.Files, f)
	}
	return u, rows.Err()
}

// uploadFile returns an upload's file and the state of its hash.
func (s *Store) uploadFile(id string, n int) (UploadFile, []byte, error) {
	var hash []byte
	var f UploadFile
	var result string
	err := s.db.QueryRow(`SELECT `+uploadFileCols+`, hash FROM upload_files WHERE upload = ? AND n = ?`, id, n).
		Scan(&f.N, &f.Path, &f.Size, &f.Unzip, &f.Modified, &f.Received, &f.SHA256, &f.Dropped, &result, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return UploadFile{}, nil, ErrUploadNotFound
	}
	return f, hash, err
}

// setReceived records the bytes of a file of an open upload received up
// to to, from from, with the state of its hash then, and its SHA-256 once
// whole. It reports false when the file had not from bytes, or the upload
// is not open any more.
func (s *Store) setReceived(id string, n int, from, to int64, hash []byte, sum string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE upload_files SET received = ?, hash = ?, sha256 = ? WHERE upload = ? AND n = ? AND received = ?
		AND (SELECT state FROM uploads WHERE id = ?) = ?`, to, hash, sum, id, n, from, id, UploadOpen)
	if err != nil {
		return false, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE uploads SET updated = ? WHERE id = ?`, nanos(now), id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// touchUpload moves an upload's last change to now.
func (s *Store) touchUpload(id, policy string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE uploads SET updated = ?, policy = ? WHERE id = ?`, nanos(now), policy, id)
	return err
}

// startCompletion turns an open upload into one completing, with its
// policy and the files dropped; it reports false when it was not open.
func (s *Store) startCompletion(id, policy string, drop []int, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE uploads SET state = ?, policy = ?, updated = ? WHERE id = ? AND state = ?`,
		UploadCompleting, policy, nanos(now), id, UploadOpen)
	if err != nil {
		return false, err
	}
	if k, _ := res.RowsAffected(); k == 0 {
		return false, nil
	}
	for _, n := range drop {
		if _, err := tx.Exec(`UPDATE upload_files SET dropped = 1 WHERE upload = ? AND n = ?`, id, n); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// setUploadBatches records the batches an upload's completion opened, by
// group, before a file joins one: a completion resumed after a restart
// keeps them.
func (s *Store) setUploadBatches(id string, batches map[string]string) error {
	data, err := json.Marshal(batches)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = s.db.Exec(`UPDATE uploads SET batches = ? WHERE id = ?`, string(data), id)
	return err
}

// endUpload records the end of an upload's completion.
func (s *Store) endUpload(id, state, msg string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE uploads SET state = ?, error = ?, updated = ? WHERE id = ?`, state, msg, nanos(now), id)
	return err
}

// deleteUpload forgets an upload and its files.
func (s *Store) deleteUpload(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM upload_files WHERE upload = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM uploads WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// uploadsWhere returns the uploads a condition selects, without their
// files, newest first.
func (s *Store) uploadsWhere(cond string, args ...any) ([]Upload, error) {
	rows, err := s.db.Query(`SELECT `+uploadCols+` FROM uploads WHERE `+cond+` ORDER BY created DESC`, args...)
	if err != nil {
		return nil, err
	}
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.db.QueryRow(`SELECT count(*), coalesce(sum(size), 0), coalesce(sum(received), 0),
			coalesce(max(instr(path, '/') > 0), 0), coalesce(min(unzip), 0) FROM upload_files WHERE upload = ?`,
			out[i].ID).Scan(&out[i].FileCount, &out[i].Bytes, &out[i].Received, &out[i].Folders, &out[i].Zips); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// openUploadLike returns the open upload of an account in a project with
// the same files, to resume.
func (s *Store) openUploadLike(project, ownerID, fingerprint string) (Upload, error) {
	list, err := s.uploadsWhere(`project = ? AND owner_id = ? AND fingerprint = ? AND state = ?`, project, ownerID, fingerprint, UploadOpen)
	if err != nil {
		return Upload{}, err
	}
	if len(list) == 0 {
		return Upload{}, ErrUploadNotFound
	}
	return list[0], nil
}

// localBytes is the bytes of the files of a project's IN folder on the
// disk, a source's left out.
func (s *Store) localBytes(project string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT coalesce(sum(max(size, 0)), 0) FROM files WHERE project = ? AND source = ?`, project, SourceLocal).Scan(&n)
	return n, err
}

// placedRow is a file an upload placed, for in.db, and the result of the
// upload's file it came from (n < 0: none, a zip's file).
type placedRow struct {
	File
	upload string
	n      int
	result *FileResult
}

// recordPlaced writes, in one transaction, the rows of the files an upload
// placed (new, or over the row of the file they replaced: status new, with
// their hash) and the results of its files.
func (s *Store) recordPlaced(rows []placedRow, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if r.Path != "" {
			if _, err := tx.Exec(`INSERT INTO files (`+fileCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, '')
				ON CONFLICT (project, path) DO UPDATE SET dir = excluded.dir, size = excluded.size, mtime = excluded.mtime,
				sha256 = excluded.sha256, status = excluded.status, run = '', source = excluded.source, batch = excluded.batch,
				updated = excluded.updated, etag = ''`,
				r.Project, r.Path, r.Dir, r.Size, nanos(r.Modified), r.SHA256, StatusNew, SourceLocal, r.Batch, nanos(now), nanos(now)); err != nil {
				return err
			}
		}
		if r.result != nil && r.n >= 0 {
			data, err := json.Marshal(r.result)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE upload_files SET result = ? WHERE upload = ? AND n = ?`, string(data), r.upload, r.n); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// closeUploadBatch records the batch an upload's completion filled in a
// group as closed now, with the files and bytes that joined it; false
// when none did.
func (s *Store) closeUploadBatch(id, project, group string, now time.Time) (Batch, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := Batch{ID: id, Project: project, Dir: group, Opened: now, LastChange: now, Closed: now}
	if err := s.db.QueryRow(`SELECT count(*), coalesce(sum(max(size, 0)), 0) FROM files WHERE batch = ?`, id).Scan(&b.Files, &b.Bytes); err != nil {
		return Batch{}, false, err
	}
	if b.Files == 0 {
		return Batch{}, false, nil
	}
	_, err := s.db.Exec(`INSERT INTO batches (`+batchCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		b.ID, project, group, nanos(now), nanos(now), nanos(now), b.Files, b.Bytes)
	return b, err == nil, err
}

// uploadProjectRenamed and uploadProjectRemoved follow a project's rename
// and deletion; the staged files move, or go, with the project's folder.
func uploadProjectRenamed(tx *sql.Tx, from, to string) error {
	if _, err := tx.Exec(`DELETE FROM upload_files WHERE upload IN (SELECT id FROM uploads WHERE project = ?)`, to); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM uploads WHERE project = ?`, to); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE uploads SET project = ? WHERE project = ?`, to, from)
	return err
}

func (s *Store) uploadProjectRemoved(project string) error {
	if _, err := s.db.Exec(`DELETE FROM upload_files WHERE upload IN (SELECT id FROM uploads WHERE project = ?)`, project); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM uploads WHERE project = ?`, project)
	return err
}

// groupOfPath is the first-level folder of the file at the vault path p of
// a project's IN folder: its batch's.
func groupOfPath(project, p string) string {
	return groupOf(dirOf(project+"/IN/", p))
}

// setUploadTicket records the SHA-256 of an upload's ticket and the token
// that minted it, in place of the one before.
func (s *Store) setUploadTicket(id, hash, tokenID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE uploads SET ticket_hash = ?, ticket_token = ? WHERE id = ?`, hash, tokenID, id)
	return err
}

// uploadTicket returns the SHA-256 of an upload's ticket and its token.
func (s *Store) uploadTicket(id string) (hash, tokenID string, err error) {
	err = s.db.QueryRow(`SELECT ticket_hash, ticket_token FROM uploads WHERE id = ?`, id).Scan(&hash, &tokenID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrUploadNotFound
	}
	return hash, tokenID, err
}
