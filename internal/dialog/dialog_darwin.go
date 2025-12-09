//go:build darwin

package dialog

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework Foundation

#import <Cocoa/Cocoa.h>

// ShowQuitConfirmation shows an NSAlert asking user to confirm quit with active uploads.
// Returns 1 if user clicked "Quit Anyway", 0 if clicked "Cancel".
int FSMVP_ShowQuitConfirmation() {
	__block int result = 0;
	
	// Must run on main thread
	dispatch_sync(dispatch_get_main_queue(), ^{
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setMessageText:@"Uploads in Progress"];
		[alert setInformativeText:@"One or more files are currently being uploaded. Do you want to quit anyway?"];
		[alert addButtonWithTitle:@"Cancel"];
		[alert addButtonWithTitle:@"Quit Anyway"];
		[alert setAlertStyle:NSAlertStyleWarning];
		
		NSModalResponse response = [alert runModal];
		
		// NSAlertFirstButtonReturn = Cancel (1000)
		// NSAlertSecondButtonReturn = Quit Anyway (1001)
		if (response == NSAlertSecondButtonReturn) {
			result = 1; // User wants to quit
		} else {
			result = 0; // User wants to cancel
		}
	});
	
	return result;
}
*/
import "C"

func confirmQuitImpl() bool {
	result := C.FSMVP_ShowQuitConfirmation()
	return result == 1
}
