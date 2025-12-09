//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c -fmodules -fobjc-arc
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

void FSMVP_ShowQuitDialog() {
	// Run on main thread using dispatch_async (non-blocking)
	dispatch_async(dispatch_get_main_queue(), ^{
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setMessageText:@"Uploads in Progress"];
		[alert setInformativeText:@"One or more files are currently being uploaded. Do you want to quit anyway?"];
		[alert addButtonWithTitle:@"Cancel"];
		[alert addButtonWithTitle:@"Quit Anyway"];
		[alert setAlertStyle:NSAlertStyleWarning];
		
		NSModalResponse response = [alert runModal];
		
		if (response == NSAlertSecondButtonReturn) {
			// User clicked "Quit Anyway" - terminate the app
			[[NSApplication sharedApplication] terminate:nil];
		}
		// If user clicked "Cancel", do nothing - window stays open
	});
}
*/
import "C"

func showQuitConfirmationDialog() {
	C.FSMVP_ShowQuitDialog()
}
