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
DECL(IGuest, IGUEST_IID)
DECL(IGuestSession, IGUESTSESSION_IID)
static IGuestSession guest_sessions[3];
static int guest_closed[3];
static int to_utf16(const char *text, PRUnichar **out);
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
GET(IConsole, IGuest, get_guest)

static nsresult get_guest_sessions(IGuest *self, PRUint32 *count, IGuestSession ***out) {
  (void)self;
  if (fail("guest_list")) return 1U << 31;
  *count = 0;
  *out = calloc(3, sizeof(IGuestSession *));
  if (!*out) return 1U << 31;
  for (int i = 0; i < 3; ++i) {
    if (guest_closed[i]) continue;
    (*out)[(*count)++] = &guest_sessions[i];
    ++references;
  }
  return 0;
}
static nsresult guest_name(IGuestSession *self, PRUnichar **out) {
  if (fail("guest_name")) return 1U << 31;
  int i = (int)(self - guest_sessions);
  const char *name = getenv(i == 0 ? "VM_TEST_NATIVE_OWNED_NAME" : "VM_TEST_NATIVE_ACTIVE_NAME");
  if (i == 1 || !name) name = "unrelated session";
  return to_utf16(name, out) ? (1U << 31) : 0;
}
static nsresult guest_user(IGuestSession *self, PRUnichar **out) {
  (void)self;
  return to_utf16(fail("guest_user") ? "foreign" : "synthetic", out) ? (1U << 31) : 0;
}
static nsresult guest_close(IGuestSession *self) {
  if (fail("guest_close")) return 1U << 31;
  int i = (int)(self - guest_sessions);
  guest_closed[i] = 1;
  fprintf(stderr, "CLOSE_GUEST %d\n", i);
  return 0;
}

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
static nsresult session_state(IMachine *self, PRUint32 *state) {
  (void)self;
  const char *s = getenv("VM_TEST_NATIVE_SESSION");
  *state = s ? (PRUint32)strtoul(s, NULL, 10) : SessionState_Unlocked;
  return fail("session_read") ? (1U << 31) : 0;
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
                                             .LockMachine = lock_machine,
                                             .GetSessionState = session_state};
static struct IConsoleVtbl console_vtable = {
    BASE(IConsole), .GetMouse = get_mouse, .GetDisplay = get_display, .GetGuest = get_guest};
static struct IMouseVtbl mouse_vtable = {
    BASE(IMouse), .GetAbsoluteSupported = abs_supported,
    .GetRelativeSupported = rel_supported, .PutMouseEvent = mouse_relative,
    .PutMouseEventAbsolute = mouse_absolute};
static struct IDisplayVtbl display_vtable = {BASE(IDisplay),
                                             .GetScreenResolution = resolution};
static struct IGuestVtbl guest_vtable = {BASE(IGuest), .GetSessions = get_guest_sessions};
static struct IGuestSessionVtbl guest_session_vtable = {
    BASE(IGuestSession), .GetName = guest_name, .GetUser = guest_user, .Close = guest_close};

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
  IGuest_object.lpVtbl = &guest_vtable;
  IGuestSession_object.lpVtbl = &guest_session_vtable;
  for (int i = 0; i < 3; ++i) guest_sessions[i].lpVtbl = &guest_session_vtable;
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
static int to_utf8(const PRUnichar *text, char **out) {
  size_t n = 0;
  while (text[n]) ++n;
  *out = calloc(n+1, 1);
  if (!*out) return -1;
  for (size_t i = 0; i < n; ++i) (*out)[i] = (char)text[i];
  return 0;
}
static void free_utf8(char *text) { free(text); }
static SAFEARRAY *array_alloc(void) { return calloc(1, sizeof(SAFEARRAY)); }
static HRESULT array_copy(IUnknown ***out, ULONG *count, SAFEARRAY *array) {
  *count = array->c;
  *out = calloc(array->c ? array->c : 1, sizeof(IUnknown *));
  if (!*out) return (HRESULT)0x8007000eU;
  for (ULONG i = 0; i < array->c; ++i) {
    (*out)[i] = ((IUnknown **)array->pv)[i];
    IUnknown_AddRef((*out)[i]);
  }
  return 0;
}
static HRESULT array_destroy(SAFEARRAY *array) {
  for (ULONG i = 0; i < array->c; ++i) IUnknown_Release(((IUnknown **)array->pv)[i]);
  free(array->pv); free(array);
  return 0;
}
static HRESULT array_free(void *memory) { free(memory); return 0; }
static VBOXCAPI functions = {.cb = sizeof(VBOXCAPI),
                             .uVersion = VBOX_CAPI_VERSION,
                             .pfnGetVersion = version,
                             .pfnClientInitialize = initialize,
                             .pfnClientUninitialize = uninitialize,
                             .pfnUtf8ToUtf16 = to_utf16,
                             .pfnUtf16Free = free_utf16,
                             .pfnComUnallocString = free_utf16,
                             .pfnUtf16ToUtf8 = to_utf8,
                             .pfnUtf8Free = free_utf8,
                             .pfnSafeArrayOutParamAlloc = array_alloc,
                             .pfnSafeArrayCopyOutIfaceParamHelper = array_copy,
                             .pfnSafeArrayDestroy = array_destroy,
                             .pfnArrayOutFree = array_free,
                             .uEndVersion = VBOX_CAPI_VERSION};

PCVBOXCAPI VBoxGetCAPIFunctions(unsigned requested) {
  if (fail("table") ||
      VBOX_CAPI_MAJOR(requested) != VBOX_CAPI_MAJOR(VBOX_CAPI_VERSION))
    return NULL;
  return &functions;
}
