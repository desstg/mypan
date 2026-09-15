package uploadutil

import "io"

const DefaultReadProgressStep = 1024 * 1024

type ReadProgress struct {
	R          io.Reader
	Base       int64
	Total      int64
	Step       int64
	Sent       int64
	lastEmit   int64
	OnProgress func(uploaded int64)
}

func (p *ReadProgress) Read(b []byte) (int, error) {
	n, err := p.R.Read(b)
	if n <= 0 {
		return n, err
	}
	p.Sent += int64(n)
	if p.OnProgress != nil {
		step := p.Step
		if step <= 0 {
			step = DefaultReadProgressStep
		}
		if p.Sent-p.lastEmit >= step || err == io.EOF {
			p.lastEmit = p.Sent
			p.OnProgress(p.Base + p.Sent)
		}
	}
	return n, err
}
