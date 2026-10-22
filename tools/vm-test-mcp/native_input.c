/* Guest input through the VirtualBox C API. */
#define _POSIX_C_SOURCE 200809L
#include "VBoxCAPIGlue.h"
#include <errno.h>
#include <limits.h>
#include <signal.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static volatile sig_atomic_t interrupted;
static void on_signal(int sig) {
  (void)sig;
  interrupted = 1;
}

static int number(const char *text, int *value) {
  char *end;
  errno = 0;
  long parsed = strtol(text, &end, 10);
  if (errno || !*text || *end || parsed < INT_MIN || parsed > INT_MAX)
    return 0;
  *value = (int)parsed;
  return 1;
}

static HRESULT require_interface(void **object, const nsID *iid) {
  IUnknown *old = (IUnknown *)*object;
  void *checked = NULL;
  if (!old)
    return (HRESULT)0x80004003U;
  HRESULT rc = IUnknown_QueryInterface(old, iid, &checked);
  IUnknown_Release(old);
  *object = checked;
  return rc;
}

#define CHECK(call)                                                            \
  do {                                                                         \
    rc = (call);                                                               \
    if (NS_FAILED(rc)) {                                                       \
      fprintf(stderr, "%s failed (0x%08x)\n", #call, (unsigned)rc);            \
      goto done;                                                               \
    }                                                                          \
  } while (0)
#define REQUIRE(object, iid)                                                   \
  CHECK(require_interface((void **)&(object), &(iid)))
#define RELEASE(object)                                                        \
  do {                                                                         \
    if (object)                                                                \
      IUnknown_Release((IUnknown *)(object));                                  \
  } while (0)

int main(int argc, char **argv) {
  int a[9], exit_code = 1, locked = 0, initialized = 0, touched = 0;
  HRESULT rc = 0;
  IVirtualBoxClient *client = NULL;
  IVirtualBox *vbox = NULL;
  ISession *session = NULL;
  IMachine *machine = NULL;
  IConsole *console = NULL;
  IMouse *mouse = NULL;
  IDisplay *display = NULL;
  PRUnichar *uuid = NULL;
  PRUint32 width = 0, height = 0, bpp = 0, status = 0;
  PRInt32 origin_x = 0, origin_y = 0;
  PRInt32 last_x = 0, last_y = 0;
  PRBool absolute = 0, relative = 0;
  static const nsID client_iid = IVIRTUALBOXCLIENT_IID;
  static const nsID vbox_iid = IVIRTUALBOX_IID;
  static const nsID session_iid = ISESSION_IID;
  static const nsID machine_iid = IMACHINE_IID;
  static const nsID console_iid = ICONSOLE_IID;
  static const nsID mouse_iid = IMOUSE_IID;
  static const nsID display_iid = IDISPLAY_IID;

  if (argc < 4 || argv[1][0] != '/' || strlen(argv[2]) != 36) {
    fprintf(stderr, "Usage: vbox-input-helper LIBRARY_DIR VM_UUID ACTION "
                    "SCREEN X Y END_X END_Y BUTTON DZ DW RELATIVE\n");
    return 2;
  }
  const char *action = argv[3];
  if (argc != 13 || (strcmp(action, "probe") && strcmp(action, "geometry") &&
                     strcmp(action, "move") && strcmp(action, "click") &&
                     strcmp(action, "drag") && strcmp(action, "scroll") &&
                     strcmp(action, "release")))
    return 2;
  for (int i = 0; i < 9; ++i)
    if (!number(argv[i + 4], &a[i]))
      return 2;
  if (a[0] < 0 || a[0] > 63 ||
      (a[5] != 0 && a[5] != 1 && a[5] != 2 && a[5] != 4) ||
      (a[8] != 0 && a[8] != 1) || a[6] < -100 || a[6] > 100 || a[7] < -100 ||
      a[7] > 100)
    return 2;
  if (setenv("VBOX_APP_HOME", argv[1], 1) != 0)
    return 1;
  if (VBoxCGlueInit()) {
    fprintf(stderr, "VirtualBox C API load failed: %s\n", g_szVBoxErrMsg);
    return 1;
  }
  if (!g_pVBoxFuncs ||
      g_pVBoxFuncs->cb < offsetof(VBOXCAPI, pfnUtf16Free) +
                             sizeof(g_pVBoxFuncs->pfnUtf16Free) ||
      (g_pVBoxFuncs->uVersion >> 16) != (VBOX_CAPI_VERSION >> 16) ||
      !g_pVBoxFuncs->pfnGetVersion || !g_pVBoxFuncs->pfnClientInitialize ||
      !g_pVBoxFuncs->pfnClientUninitialize || !g_pVBoxFuncs->pfnUtf8ToUtf16 ||
      !g_pVBoxFuncs->pfnUtf16Free) {
    fprintf(stderr, "Unsupported VirtualBox C function table\n");
    goto done;
  }
  CHECK(g_pVBoxFuncs->pfnClientInitialize(IVIRTUALBOXCLIENT_IID_STR, &client));
  initialized = 1;
  REQUIRE(client, client_iid);
  CHECK(IVirtualBoxClient_GetVirtualBox(client, &vbox));
  REQUIRE(vbox, vbox_iid);
  CHECK(IVirtualBoxClient_GetSession(client, &session));
  REQUIRE(session, session_iid);
  CHECK(g_pVBoxFuncs->pfnUtf8ToUtf16(argv[2], &uuid));
  CHECK(IVirtualBox_FindMachine(vbox, uuid, &machine));
  REQUIRE(machine, machine_iid);
  CHECK(IMachine_LockMachine(machine, session, LockType_Shared));
  locked = 1;
  CHECK(ISession_GetConsole(session, &console));
  REQUIRE(console, console_iid);
  CHECK(IConsole_GetMouse(console, &mouse));
  REQUIRE(mouse, mouse_iid);
  CHECK(IMouse_GetAbsoluteSupported(mouse, &absolute));
  CHECK(IMouse_GetRelativeSupported(mouse, &relative));
  if (!strcmp(action, "release")) {
    CHECK(IMouse_PutMouseEvent(mouse, 0, 0, 0, 0, 0));
    puts("{\"released\":true}");
    exit_code = 0;
    goto done;
  }
  CHECK(IConsole_GetDisplay(console, &display));
  REQUIRE(display, display_iid);
  CHECK(IDisplay_GetScreenResolution(display, (PRUint32)a[0], &width, &height,
                                     &bpp, &origin_x, &origin_y, &status));
  if (!width || !height || status == GuestMonitorStatus_Disabled) {
    fprintf(stderr, "Selected display has no active framebuffer\n");
    goto done;
  }
  int read_only = !strcmp(action, "probe") || !strcmp(action, "geometry");
  if (!read_only) {
    if ((a[8] && !relative) || (!a[8] && !absolute)) {
      fprintf(stderr, "Requested mouse coordinate mode is unavailable\n");
      goto done;
    }
    if (!a[8] && (a[1] < 0 || a[2] < 0 || (PRUint32)a[1] >= width ||
                  (PRUint32)a[2] >= height ||
                  (!strcmp(action, "drag") &&
                   (a[3] < 0 || a[4] < 0 || (PRUint32)a[3] >= width ||
                    (PRUint32)a[4] >= height)))) {
      fprintf(stderr,
              "Mouse coordinates are outside the selected screenshot\n");
      goto done;
    }
    /* Absolute API coordinates use a one-based virtual desktop origin. */
    int64_t x = (int64_t)origin_x + a[1] + 1, y = (int64_t)origin_y + a[2] + 1;
    int64_t ex = (int64_t)origin_x + a[3] + 1,
            ey = (int64_t)origin_y + a[4] + 1;
    if (!a[8] && (x < INT_MIN || x > INT_MAX || y < INT_MIN || y > INT_MAX ||
                  ex < INT_MIN || ex > INT_MAX || ey < INT_MIN || ey > INT_MAX))
      goto done;
    signal(SIGTERM, on_signal);
    signal(SIGINT, on_signal);
    last_x = (PRInt32)x;
    last_y = (PRInt32)y;
    touched = 1;
    PRInt32 buttons =
        (!strcmp(action, "click") || !strcmp(action, "drag")) ? a[5] : 0;
    /* Let the guest observe the position before the button transition. */
    if (buttons) {
      if (a[8])
        CHECK(IMouse_PutMouseEvent(mouse, a[1], a[2], 0, 0, 0));
      else
        CHECK(IMouse_PutMouseEventAbsolute(mouse, (int)x, (int)y, 0, 0, 0));
      struct timespec settle = {0, 150000000};
      nanosleep(&settle, NULL);
      if (interrupted)
        goto done;
    }
    if (a[8])
      CHECK(IMouse_PutMouseEvent(mouse, buttons ? 0 : a[1], buttons ? 0 : a[2],
                                 a[6], a[7], buttons));
    else
      CHECK(IMouse_PutMouseEventAbsolute(mouse, (int)x, (int)y, a[6], a[7],
                                         buttons));
    if (!strcmp(action, "drag")) {
      struct timespec press = {0, 50000000};
      nanosleep(&press, NULL);
      int previous_x = 0, previous_y = 0;
      for (int step = 1; step <= 20 && !interrupted; ++step) {
        struct timespec delay = {0, 10000000};
        nanosleep(&delay, NULL);
        if (a[8]) {
          int nx = (int)((int64_t)a[3] * step / 20),
              ny = (int)((int64_t)a[4] * step / 20);
          CHECK(IMouse_PutMouseEvent(mouse, nx - previous_x, ny - previous_y, 0,
                                     0, buttons));
          previous_x = nx;
          previous_y = ny;
        } else {
          last_x = (PRInt32)(x + (ex - x) * step / 20);
          last_y = (PRInt32)(y + (ey - y) * step / 20);
          CHECK(IMouse_PutMouseEventAbsolute(mouse, last_x, last_y, 0, 0,
                                             buttons));
        }
      }
    }
    if (!strcmp(action, "click")) {
      struct timespec hold = {0, 50000000};
      nanosleep(&hold, NULL);
    }
    if (a[8])
      CHECK(IMouse_PutMouseEvent(mouse, 0, 0, 0, 0, 0));
    else
      CHECK(IMouse_PutMouseEventAbsolute(mouse, last_x, last_y, 0, 0, 0));
    touched = 0;
    if (interrupted)
      goto done;
  }
  printf("{\"screen\":%d,\"width\":%u,\"height\":%u,\"origin_x\":%d,\"origin_"
         "y\":%d,"
         "\"absolute_supported\":%s,\"relative_supported\":%s,\"runtime_"
         "version\":%u}\n",
         a[0], (unsigned)width, (unsigned)height, (int)origin_x, (int)origin_y,
         absolute ? "true" : "false", relative ? "true" : "false",
         g_pVBoxFuncs->pfnGetVersion());
  exit_code = 0;
done:
  if (touched && mouse) {
    rc = a[8] ? IMouse_PutMouseEvent(mouse, 0, 0, 0, 0, 0)
              : IMouse_PutMouseEventAbsolute(mouse, last_x, last_y, 0, 0, 0);
    if (NS_FAILED(rc))
      fprintf(stderr, "Mouse release failed (0x%08x)\n", (unsigned)rc);
  }
  RELEASE(display);
  RELEASE(mouse);
  RELEASE(console);
  if (locked) {
    rc = ISession_UnlockMachine(session);
    if (NS_FAILED(rc)) {
      fprintf(stderr, "Session unlock failed (0x%08x)\n", (unsigned)rc);
      exit_code = 1;
    }
  }
  RELEASE(machine);
  RELEASE(session);
  RELEASE(vbox);
  RELEASE(client);
  if (uuid)
    g_pVBoxFuncs->pfnUtf16Free(uuid);
  if (initialized)
    g_pVBoxFuncs->pfnClientUninitialize();
  VBoxCGlueTerm();
  return exit_code;
}
