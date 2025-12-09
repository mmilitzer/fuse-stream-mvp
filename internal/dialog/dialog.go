package dialog

// ConfirmQuit shows a confirmation dialog asking if the user wants to quit
// despite having active uploads. Returns true if user wants to quit, false otherwise.
func ConfirmQuit() bool {
	return confirmQuitImpl()
}
