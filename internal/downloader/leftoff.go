package downloader

import (
	"sort"
	"sync/atomic"
)

// LeftOffMod is a client-only mod that was kept off the server.
type LeftOffMod struct {
	ProjectID int
	FileID    int
	Slug      string // CurseForge slug, when the exclude list named the mod
	FileName  string // jar name, when it was looked up
	ByList    bool   // named by the exclude list or --exclude-mods rather than tagged Client by CurseForge
}

// noteLeftOff remembers a mod kept off the server. A mod seen twice (skipped
// on download, then removed by the cleaner) is kept once, with what each
// sighting learned about it.
func (d *Downloader) noteLeftOff(m LeftOffMod) {
	d.leftOffMu.Lock()
	defer d.leftOffMu.Unlock()
	if d.leftOff == nil {
		d.leftOff = make(map[int]*LeftOffMod)
	}
	prev, ok := d.leftOff[m.ProjectID]
	if !ok {
		d.leftOff[m.ProjectID] = &m
		return
	}
	if prev.FileName == "" {
		prev.FileName = m.FileName
	}
	if prev.Slug == "" {
		prev.Slug = m.Slug
	}
	prev.ByList = prev.ByList || m.ByList
}

// LeftOff returns the client-only mods kept off the server so far, ordered by
// project ID.
func (d *Downloader) LeftOff() []LeftOffMod {
	d.leftOffMu.Lock()
	defer d.leftOffMu.Unlock()
	out := make([]LeftOffMod, 0, len(d.leftOff))
	for _, m := range d.leftOff {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProjectID < out[j].ProjectID })
	return out
}

// progress counts finished download tasks for OnModDone.
type progress struct {
	d     *Downloader
	done  atomic.Int64
	total int
}

func (d *Downloader) newProgress(total int) *progress {
	p := &progress{d: d, total: total}
	if d.OnModDone != nil {
		d.OnModDone(0, total)
	}
	return p
}

// step records one finished task, whether it was downloaded, skipped or failed.
func (p *progress) step() {
	n := p.done.Add(1)
	if p.d.OnModDone != nil {
		p.d.OnModDone(int(n), p.total)
	}
}
