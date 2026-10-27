// C interface between the Go UI code and the AppKit layer in mac_darwin.m.
// Objects cross the boundary as retained handles (SDHandle) that Go releases
// through the matching *_destroy / *_release calls.

#ifndef SHOREDROP_MAC_H
#define SHOREDROP_MAC_H

#include <stdint.h>

typedef uintptr_t SDHandle;

// application
void sd_app_init(void);
void sd_app_run(void);
void sd_app_stop(void);
void sd_app_wake(void);
void sd_app_activate(void);
int sd_system_dark(void);
void sd_app_set_appearance(int dark);
int sd_mouse_buttons(void);
unsigned long sd_modifier_flags(void);
void sd_alert(const char *msg);

// windows (kind 0 = main window, 1 = dialog)
SDHandle sd_win_new(int id, int kind, const char *title, double w, double h, SDHandle owner, int resizable);
void sd_win_show(SDHandle h);
void sd_win_destroy(SDHandle h, SDHandle owner);
void sd_win_invalidate(SDHandle h);
void sd_win_set_title(SDHandle h, const char *t);
void sd_win_content_size(SDHandle h, double *w, double *hh);
int sd_win_is_zoomed(SDHandle h);
void sd_win_zoom(SDHandle h);
void sd_win_miniaturize(SDHandle h);
void sd_win_set_min(SDHandle h, double w, double hh);
void sd_win_focus_content(SDHandle h);
void sd_win_to_screen(SDHandle h, double x, double y, double *sx, double *sy);
void sd_win_from_screen(SDHandle h, double sx, double sy, double *x, double *y);
void sd_win_get_frame(SDHandle h, double *x, double *y, double *w, double *hh);
void sd_win_set_frame(SDHandle h, double x, double y, double w, double hh);
void sd_win_center(SDHandle h);
void sd_win_accept_drops(SDHandle h);
void sd_win_run_modal(SDHandle h);
void sd_win_stop_modal(void);
double sd_win_chrome_left(SDHandle h);
int sd_win_is_key(SDHandle h);

// timers
SDHandle sd_timer_new(int win, uintptr_t id, double seconds);
void sd_timer_kill(SDHandle t);

// cursors use the Win32 IDC_* ids from keys.go
void sd_set_cursor(int idc);

// drawing; ctx is the CGContextRef handed to goDraw, colours are 0..1
void sd_fill_rect(void *ctx, double x, double y, double w, double h, double r, double g, double b, double a);
void sd_fill_round(void *ctx, double x, double y, double w, double h, double rad, double r, double g, double b, double a);
void sd_stroke_round(void *ctx, double x, double y, double w, double h, double rad, double width, double r, double g, double b, double a);
void sd_fill_ellipse(void *ctx, double x, double y, double w, double h, double r, double g, double b, double a);
void sd_stroke_ellipse(void *ctx, double x, double y, double w, double h, double width, double r, double g, double b, double a);
void sd_path(void *ctx, const double *pts, int n, int fill, double width, double r, double g, double b, double a);
void sd_push_clip(void *ctx, double x, double y, double w, double h);
void sd_pop_clip(void *ctx);

// text (fonts are CTFontRef handles; kind 0 = system UI, 1 = monospaced)
SDHandle sd_font_new(double size, int weight, int kind);
void sd_font_metrics(SDHandle f, double *ascent, double *descent, double *leading);
double sd_text_width(SDHandle f, const char *s);
void sd_draw_text(void *ctx, SDHandle f, const char *s, double x, double y, double w, double h, int align, int middle,
                  double r, double g, double b, double a);
double sd_wrap(void *ctx, SDHandle f, const char *s, double x, double y, double w, double maxh,
               double r, double g, double b, double a);
int sd_draw_symbol(void *ctx, const char *name, double pt, double x, double y, double w, double h,
                   double r, double g, double b, double a);

// text boxes
SDHandle sd_tb_new(SDHandle win, int id, int password, int multiline);
void sd_tb_set_frame(SDHandle tb, double x, double y, double w, double h, int visible);
void sd_tb_set_font(SDHandle tb, SDHandle font);
void sd_tb_set_colors(SDHandle tb, double r, double g, double b, double pr, double pg, double pb);
char *sd_tb_text(SDHandle tb);
void sd_tb_set_text(SDHandle tb, const char *s);
void sd_tb_set_placeholder(SDHandle tb, const char *s);
void sd_tb_select(SDHandle tb, long start, long end);
void sd_tb_set_state(SDHandle tb, int editable, int enabled);
void sd_tb_focus(SDHandle win, SDHandle tb);
void sd_tb_destroy(SDHandle tb);

// menus (flags: 1 checked, 2 disabled, 4 separator)
SDHandle sd_menu_new(const char *title);
void sd_menu_release(SDHandle m);
void sd_menu_clear(uintptr_t m);
void sd_menu_add(uintptr_t m, const char *label, const char *key, unsigned long mods, long tag, int flags);
void sd_menu_add_std(uintptr_t m, const char *label, const char *selector, const char *key, unsigned long mods);
long sd_menu_popup(SDHandle m, SDHandle win, double x, double y, double minWidth);
void sd_menubar_add(SDHandle m, int index, int role);

// shell
void sd_clipboard_set(const char *s);
void sd_open_path(const char *p);
void sd_reveal_path(const char *p);
char *sd_trash_path(const char *p);
char *sd_pick(SDHandle owner, const char *title, const char *initial, int folders);

#endif
