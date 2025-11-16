#include "drag_linux.h"
#include <gtk/gtk.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    GtkWidget *window;
    GtkWidget *event_box;
    GMainLoop *loop;
    char *file_uri;
    int result;
    gboolean drag_started;
} DragContext;

// Callback for when drag ends
static void on_drag_end(GtkWidget *widget, GdkDragContext *context, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    ctx->drag_started = TRUE;
    
    if (ctx->loop && g_main_loop_is_running(ctx->loop)) {
        g_main_loop_quit(ctx->loop);
    }
}

// Callback to provide drag data
static void on_drag_data_get(GtkWidget *widget, GdkDragContext *context,
                             GtkSelectionData *selection_data, guint info,
                             guint time, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    // Set the URI list data
    gtk_selection_data_set_uris(selection_data, (gchar*[]){ctx->file_uri, NULL});
}

// Callback for drag begin to set icon
static void on_drag_begin(GtkWidget *widget, GdkDragContext *context, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    ctx->drag_started = TRUE;
    
    // Set a generic document icon for the drag operation
    GtkIconTheme *icon_theme = gtk_icon_theme_get_default();
    GdkPixbuf *icon_pixbuf = gtk_icon_theme_load_icon(
        icon_theme,
        "text-x-generic",
        48,
        0,
        NULL
    );
    
    if (icon_pixbuf) {
        gtk_drag_set_icon_pixbuf(context, icon_pixbuf, 24, 24);
        g_object_unref(icon_pixbuf);
    }
}

// Callback to handle button press and initiate drag
static gboolean on_button_press(GtkWidget *widget, GdkEventButton *event, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    if (event->button == 1) { // Left mouse button
        // Start the drag operation
        GdkDragContext *context = gtk_drag_begin_with_coordinates(
            widget,
            gtk_drag_source_get_target_list(widget),
            GDK_ACTION_COPY,
            1,
            (GdkEvent*)event,
            event->x,
            event->y
        );
        
        return TRUE;
    }
    
    return FALSE;
}

// Timeout to destroy window if drag doesn't start
static gboolean on_timeout(gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    if (ctx->loop && g_main_loop_is_running(ctx->loop)) {
        if (!ctx->drag_started) {
            ctx->result = 4; // Timeout
        }
        g_main_loop_quit(ctx->loop);
    }
    
    return G_SOURCE_REMOVE;
}

int FSMVP_StartFileDrag(const char* path) {
    if (!path || strlen(path) == 0) {
        return 1; // Invalid path
    }
    
    // Initialize GTK if not already initialized
    if (!gtk_init_check(NULL, NULL)) {
        return 2; // GTK initialization failed
    }
    
    // Convert file path to URI format (file://...)
    char *file_uri = g_filename_to_uri(path, NULL, NULL);
    if (!file_uri) {
        return 3; // Failed to convert path to URI
    }
    
    // Create drag context
    DragContext ctx = {
        .window = NULL,
        .event_box = NULL,
        .loop = NULL,
        .file_uri = file_uri,
        .result = 0,
        .drag_started = FALSE
    };
    
    // Create a small window at the cursor position
    ctx.window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
    gtk_window_set_decorated(GTK_WINDOW(ctx.window), FALSE);
    gtk_window_set_default_size(GTK_WINDOW(ctx.window), 64, 64);
    gtk_window_set_type_hint(GTK_WINDOW(ctx.window), GDK_WINDOW_TYPE_HINT_UTILITY);
    
    // Create an event box to capture mouse events
    ctx.event_box = gtk_event_box_new();
    gtk_widget_add_events(ctx.event_box, GDK_BUTTON_PRESS_MASK);
    
    // Create an icon as the drag source
    GtkWidget *icon = gtk_image_new_from_icon_name("text-x-generic", GTK_ICON_SIZE_DIALOG);
    gtk_container_add(GTK_CONTAINER(ctx.event_box), icon);
    gtk_container_add(GTK_CONTAINER(ctx.window), ctx.event_box);
    
    // Set up the drag source with text/uri-list target
    GtkTargetEntry targets[] = {
        {"text/uri-list", 0, 0}
    };
    gtk_drag_source_set(
        ctx.event_box,
        GDK_BUTTON1_MASK,
        targets,
        1,
        GDK_ACTION_COPY
    );
    
    // Connect drag signals
    g_signal_connect(ctx.event_box, "drag-data-get", G_CALLBACK(on_drag_data_get), &ctx);
    g_signal_connect(ctx.event_box, "drag-begin", G_CALLBACK(on_drag_begin), &ctx);
    g_signal_connect(ctx.event_box, "drag-end", G_CALLBACK(on_drag_end), &ctx);
    g_signal_connect(ctx.event_box, "button-press-event", G_CALLBACK(on_button_press), &ctx);
    
    // Position window at cursor (if possible)
    GdkDisplay *display = gdk_display_get_default();
    if (display) {
        GdkDeviceManager *device_manager = gdk_display_get_device_manager(display);
        if (device_manager) {
            GdkDevice *pointer = gdk_device_manager_get_client_pointer(device_manager);
            if (pointer) {
                gint x, y;
                gdk_device_get_position(pointer, NULL, &x, &y);
                gtk_window_move(GTK_WINDOW(ctx.window), x - 32, y - 32);
            }
        }
    }
    
    // Show the window and all its children
    gtk_widget_show_all(ctx.window);
    
    // Set a timeout to close the window if no drag starts
    guint timeout_id = g_timeout_add_seconds(10, on_timeout, &ctx);
    
    // Run event loop until drag completes or timeout
    ctx.loop = g_main_loop_new(NULL, FALSE);
    g_main_loop_run(ctx.loop);
    
    // Cleanup
    g_source_remove(timeout_id);
    g_main_loop_unref(ctx.loop);
    
    if (ctx.window) {
        gtk_widget_destroy(ctx.window);
    }
    
    g_free(file_uri);
    
    return ctx.result;
}
