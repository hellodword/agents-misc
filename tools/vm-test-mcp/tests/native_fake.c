/* Synthetic C API runtime for ABI, reference ownership and input tests. */
#include "VBoxCAPIGlue.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static int references, locks;
static int fail(const char *name) {
  const char *mode = getenv("VM_TEST_NATIVE_FAIL");
  return mode && !strcmp(mode, name);
}
#define DECL(type, iid_value)                                                  \
  static type type##_object;                                                   \
  static nsresult type##_query(type *self, const nsID *iid, void **out) {      \
    static const nsID expected = iid_value;                                    \
    if (fail(#type) || memcmp(iid, &expected, sizeof(expected))) {             \
      *out = NULL;                                                             \
      return (nsresult)0x80004002U;                                            \
    }                                                                          \
    *out = self;                                                               \
    ++references;                                                              \
    return 0;                                                                  \
  }                                                                            \
  static nsrefcnt type##_add(type *self) {                                     \
    (void)self;                                                                \
    return (nsrefcnt)++references;                                             \
  }                                                                            \
  static nsrefcnt type##_release(type *self) {                                 \
    (void)self;                                                                \
    return (nsrefcnt)--references;                                             \
  }
DECL(IVirtualBoxClient, IVIRTUALBOXCLIENT_IID)
DECL(IVirtualBox, IVIRTUALBOX_IID)
DECL(ISession, ISESSION_IID)
DECL(IMachine, IMACHINE_IID)
DECL(IConsole, ICONSOLE_IID)
DECL(IMouse, IMOUSE_IID)
DECL(IDisplay, IDISPLAY_IID)
#define BASE(type)                                                             \
  .QueryInterface = type##_query, .AddRef = type##_add,                        \
  .Release = type##_release
#define GET(from, to, method)                                                  \
  static nsresult method(from *self, to **out) {                               \
    (void)self;                                                                \
    *out = &to##_object;                                                       \
    ++references;                                                              \
    return 0;                                                                  \
  }
GET(IVirtualBoxClient, IVirtualBox, get_vbox)
GET(IVirtualBoxClient, ISession, get_session)
GET(ISession, IConsole, get_console)
GET(IConsole, IMouse, get_mouse)
GET(IConsole, IDisplay, get_display)

static nsresult find_machine(IVirtualBox *self, PRUnichar *uuid,
                             IMachine **out) {
  (void)self;
  (void)uuid;
  *out = &IMachine_object;
  ++references;
  return 0;
}
static nsresult lock_machine(IMachine *self, ISession *session, PRUint32 mode) {
  (void)self;
  (void)session;
  if (mode != LockType_Shared)
    return 1U << 31;
  ++locks;
  return 0;
}
static nsresult unlock_machine(ISession *self) {
  (void)self;
  --locks;
  return 0;
}
static nsresult abs_supported(IMouse *self, PRBool *out) {
  (void)self;
  *out = !fail("absolute");
  return 0;
}
static nsresult rel_supported(IMouse *self, PRBool *out) {
  (void)self;
  *out = 1;
  return 0;
}
static nsresult mouse_relative(IMouse *self, PRInt32 x, PRInt32 y, PRInt32 dz,
                               PRInt32 dw, PRInt32 buttons) {
  (void)self;
  fprintf(stderr, "REL %d %d %d %d %d\n", x, y, dz, dw, buttons);
  return 0;
}
static nsresult mouse_absolute(IMouse *self, PRInt32 x, PRInt32 y, PRInt32 dz,
                               PRInt32 dw, PRInt32 buttons) {
  (void)self;
  fprintf(stderr, "ABS %d %d %d %d %d\n", x, y, dz, dw, buttons);
  return fail("send") ? (1U << 31) : 0;
}
static nsresult resolution(IDisplay *self, PRUint32 screen, PRUint32 *width,
                           PRUint32 *height, PRUint32 *bpp, PRInt32 *x,
                           PRInt32 *y, PRUint32 *status) {
  (void)self;
  if (screen > 1)
    return 1U << 31;
  *width = 1920;
  *height = 1080;
  *bpp = 32;
  *x = screen ? -1920 : 0;
  *y = 0;
  *status = GuestMonitorStatus_Enabled;
  return 0;
}

static struct IVirtualBoxClientVtbl client_vtable = {BASE(IVirtualBoxClient),
                                                     .GetVirtualBox = get_vbox,
                                                     .GetSession = get_session};
static struct IVirtualBoxVtbl vbox_vtable = {BASE(IVirtualBox),
                                             .FindMachine = find_machine};
static struct ISessionVtbl session_vtable = {
    BASE(ISession), .GetConsole = get_console, .UnlockMachine = unlock_machine};
static struct IMachineVtbl machine_vtable = {BASE(IMachine),
                                             .LockMachine = lock_machine};
static struct IConsoleVtbl console_vtable = {
    BASE(IConsole), .GetMouse = get_mouse, .GetDisplay = get_display};
static struct IMouseVtbl mouse_vtable = {
    BASE(IMouse), .GetAbsoluteSupported = abs_supported,
    .GetRelativeSupported = rel_supported, .PutMouseEvent = mouse_relative,
    .PutMouseEventAbsolute = mouse_absolute};
static struct IDisplayVtbl display_vtable = {BASE(IDisplay),
                                             .GetScreenResolution = resolution};

static unsigned version(void) {
  const char *v = getenv("VM_TEST_NATIVE_VERSION");
  return v ? (unsigned)strtoul(v, NULL, 10) : 7002020;
}
static HRESULT initialize(const char *iid, IVirtualBoxClient **out) {
  if (strcmp(iid, IVIRTUALBOXCLIENT_IID_STR))
    return 1U << 31;
  IVirtualBoxClient_object.lpVtbl = &client_vtable;
  IVirtualBox_object.lpVtbl = &vbox_vtable;
  ISession_object.lpVtbl = &session_vtable;
  IMachine_object.lpVtbl = &machine_vtable;
  IConsole_object.lpVtbl = &console_vtable;
  IMouse_object.lpVtbl = &mouse_vtable;
  IDisplay_object.lpVtbl = &display_vtable;
  *out = &IVirtualBoxClient_object;
  ++references;
  return 0;
}
static void uninitialize(void) {
  fprintf(stderr, "REFS %d LOCKS %d\n", references, locks);
}
static int to_utf16(const char *text, PRUnichar **out) {
  size_t n = strlen(text);
  *out = calloc(n + 1, sizeof(PRUnichar));
  if (!*out)
    return -1;
  for (size_t i = 0; i < n; ++i) {
    (*out)[i] = (PRUnichar)text[i];
  }
  return 0;
}
static void free_utf16(PRUnichar *text) { free(text); }
static VBOXCAPI functions = {.cb = sizeof(VBOXCAPI),
                             .uVersion = VBOX_CAPI_VERSION,
                             .pfnGetVersion = version,
                             .pfnClientInitialize = initialize,
                             .pfnClientUninitialize = uninitialize,
                             .pfnUtf8ToUtf16 = to_utf16,
                             .pfnUtf16Free = free_utf16,
                             .uEndVersion = VBOX_CAPI_VERSION};

PCVBOXCAPI VBoxGetCAPIFunctions(unsigned requested) {
  if (fail("table") ||
      VBOX_CAPI_MAJOR(requested) != VBOX_CAPI_MAJOR(VBOX_CAPI_VERSION))
    return NULL;
  return &functions;
}
