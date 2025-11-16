#include "drag_linux.h"
#include <gtk/gtk.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    GtkWidget *window;
    GMainLoop *loop;
    char *file_uri;
    int result;
} DragContext;

// Callback for when drag ends
static void on_drag_end(GtkDragSource *source, GdkDrag *drag, gboolean delete_data, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    if (ctx->loop && g_main_loop_is_running(ctx->loop)) {
        g_main_loop_quit(ctx->loop);
    }
}

// Callback to prepare drag content
static GdkContentProvider* on_prepare(GtkDragSource *source, double x, double y, gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    // Create a GFile from the URI
    GFile *file = g_file_new_for_uri(ctx->file_uri);
    
    // Build a GSList containing the single file
    GSList *file_list = NULL;
    file_list = g_slist_append(file_list, file);
    
    // Create content provider for the file list
    GdkContentProvider *provider = gdk_content_provider_new_union(
        (GdkContentProvider*[]){
            gdk_content_provider_new_typed(GDK_TYPE_FILE_LIST, file_list),
        },
        1
    );
    
    // Cleanup
    g_slist_free(file_list);
    g_object_unref(file);
    
    return provider;
}

// Callback for drag begin to set icon
static void on_drag_begin(GtkDragSource *source, GdkDrag *drag, gpointer user_data) {
    // Set a generic document icon for the drag operation
    GtkIconTheme *icon_theme = gtk_icon_theme_get_for_display(gdk_display_get_default());
    GtkIconPaintable *icon_paintable = gtk_icon_theme_lookup_icon(
        icon_theme,
        "text-x-generic",
        NULL,
        48,
        1,
        GTK_TEXT_DIR_NONE,
        0
    );
    
    if (icon_paintable) {
        gtk_drag_source_set_icon(source, GDK_PAINTABLE(icon_paintable), 24, 24);
        g_object_unref(icon_paintable);
    }
}

// Timeout to destroy window if drag doesn't start
static gboolean on_timeout(gpointer user_data) {
    DragContext *ctx = (DragContext*)user_data;
    
    if (ctx->loop && g_main_loop_is_running(ctx->loop)) {
        ctx->result = 4; // Timeout
        g_main_loop_quit(ctx->loop);
    }
    
    return G_SOURCE_REMOVE;
}

int FSMVP_StartFileDrag(const char* path) {
    if (!path || strlen(path) == 0) {
        return 1; // Invalid path
    }
    
    // Initialize GTK if not already initialized
    if (!gtk_is_initialized()) {
        if (!gtk_init_check()) {
            return 2; // GTK initialization failed
        }
    }
    
    // Convert file path to URI format (file://...)
    char *file_uri = g_filename_to_uri(path, NULL, NULL);
    if (!file_uri) {
        return 3; // Failed to convert path to URI
    }
    
    // Create drag context
    DragContext ctx = {
        .window = NULL,
        .loop = NULL,
        .file_uri = file_uri,
        .result = 0
    };
    
    // Create a small window at the cursor position
    ctx.window = gtk_window_new();
    gtk_window_set_decorated(GTK_WINDOW(ctx.window), FALSE);
    gtk_window_set_default_size(GTK_WINDOW(ctx.window), 64, 64);
    
    // Create a box with an icon as the drag source
    GtkWidget *box = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
    GtkWidget *icon = gtk_image_new_from_icon_name("text-x-generic");
    gtk_image_set_pixel_size(GTK_IMAGE(icon), 48);
    gtk_box_append(GTK_BOX(box), icon);
    gtk_window_set_child(GTK_WINDOW(ctx.window), box);
    
    // Create and configure the drag source
    GtkDragSource *drag_source = gtk_drag_source_new();
    gtk_drag_source_set_actions(drag_source, GDK_ACTION_COPY);
    
    // Connect drag signals
    g_signal_connect(drag_source, "prepare", G_CALLBACK(on_prepare), &ctx);
    g_signal_connect(drag_source, "drag-begin", G_CALLBACK(on_drag_begin), &ctx);
    g_signal_connect(drag_source, "drag-end", G_CALLBACK(on_drag_end), &ctx);
    
    // Add drag source to the icon widget
    gtk_widget_add_controller(icon, GTK_EVENT_CONTROLLER(drag_source));
    
    // Position window at cursor (if possible)
    GdkDisplay *display = gdk_display_get_default();
    if (display) {
        GdkSeat *seat = gdk_display_get_default_seat(display);
        if (seat) {
            GdkDevice *pointer = gdk_seat_get_pointer(seat);
            if (pointer) {
                double x, y;
                gdk_device_get_surface_at_position(pointer, &x, &y);
                // Note: GTK4 doesn't allow precise window positioning on Wayland
                // This is a limitation of the Wayland protocol for security reasons
            }
        }
    }
    
    // Show the window
    gtk_window_present(GTK_WINDOW(ctx.window));
    
    // Set a timeout to close the window if no drag starts
    guint timeout_id = g_timeout_add_seconds(10, on_timeout, &ctx);
    
    // Run event loop until drag completes or timeout
    ctx.loop = g_main_loop_new(NULL, FALSE);
    g_main_loop_run(ctx.loop);
    
    // Cleanup
    g_source_remove(timeout_id);
    g_main_loop_unref(ctx.loop);
    
    if (ctx.window) {
        gtk_window_destroy(GTK_WINDOW(ctx.window));
    }
    
    g_free(file_uri);
    
    return ctx.result;
}
