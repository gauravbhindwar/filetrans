package transfer

// Callbacks receives transfer progress events.
// Any nil field is silently skipped. Pass nil for the whole struct to use
// the default terminal progress bar.
type Callbacks struct {
	OnFileStart    func(name string, size int64)
	OnProgress     func(name string, transferred int64, speed float64, etaSec float64)
	OnFileComplete func(name string)
	OnFileError    func(name string, err error)
	OnSessionDone  func()
	OnLog          func(level, msg string)
}

func (cb *Callbacks) fileStart(name string, size int64) { cb.FileStart(name, size) }
func (cb *Callbacks) progress(name string, transferred int64, speed, etaSec float64) {
	cb.Progress(name, transferred, speed, etaSec)
}
func (cb *Callbacks) fileComplete(name string)      { cb.FileComplete(name) }
func (cb *Callbacks) fileError(name string, err error) { cb.FileError(name, err) }
func (cb *Callbacks) sessionDone()                  { cb.SessionDone() }
func (cb *Callbacks) log(level, msg string)         { cb.Log(level, msg) }

// Exported variants for use by packages outside transfer (e.g. gtp).

func (cb *Callbacks) FileStart(name string, size int64) {
	if cb != nil && cb.OnFileStart != nil {
		cb.OnFileStart(name, size)
	}
}

func (cb *Callbacks) Progress(name string, transferred int64, speed, etaSec float64) {
	if cb != nil && cb.OnProgress != nil {
		cb.OnProgress(name, transferred, speed, etaSec)
	}
}

func (cb *Callbacks) FileComplete(name string) {
	if cb != nil && cb.OnFileComplete != nil {
		cb.OnFileComplete(name)
	}
}

func (cb *Callbacks) FileError(name string, err error) {
	if cb != nil && cb.OnFileError != nil {
		cb.OnFileError(name, err)
	}
}

func (cb *Callbacks) SessionDone() {
	if cb != nil && cb.OnSessionDone != nil {
		cb.OnSessionDone()
	}
}

func (cb *Callbacks) Log(level, msg string) {
	if cb != nil && cb.OnLog != nil {
		cb.OnLog(level, msg)
	}
}
