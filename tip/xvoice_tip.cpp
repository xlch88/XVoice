// XVoice text service: a TSF speech-category TIP that TSF activates next to
// whatever keyboard IME is in use. It does nothing until xvoice.exe talks to
// it: for each thread that gets a text focus it creates a message-only window
// ("XVoiceTipWindow", window text = protocol version) that accepts
// WM_COPYDATA commands on that UI thread.
//
// It runs inside every GUI process for as long as they live, so it is kept to
// the bare mechanism: all policy is in xvoice.exe, nothing here blocks, and
// every edit is queued asynchronously. Old copies stay loaded in long-running
// processes after an update, so the protocol only ever grows.
//
// Protocol 1 (COPYDATASTRUCT.dwData, lpData = UTF-16 text without terminator):
//   QUERY   -> (generation << 2) | state; state 1 editable, 2 not writable
//              (read-only, or the application disabled keyboard input as it
//              does for passwords), 0 when the thread has no text focus.
//              generation changes whenever the focus moves or someone else
//              ends our composition.
//   COMPOSE -> show text as the composition, starting one if needed.
//   COMMIT  -> replace the composition with text (if any) and end it.
// COMPOSE and COMMIT return 1 when the edit was queued.
//
// WM_COPYDATA from lower-integrity processes stays blocked (UIPI): letting it
// through would let any program type into elevated windows.
#include <windows.h>
#include <msctf.h>
#include <olectl.h>
#include <new>
#include <string>

namespace {

// {B7E3A1C4-5D2F-4A8B-9C61-3E7F0D4A2B95}
const CLSID CLSID_XVoiceTip = {0xb7e3a1c4, 0x5d2f, 0x4a8b, {0x9c, 0x61, 0x3e, 0x7f, 0x0d, 0x4a, 0x2b, 0x95}};
// {D2A64F18-7B3C-4E95-A1D7-5C8E2F0B6A43}
const GUID PROFILE_XVoiceTip = {0xd2a64f18, 0x7b3c, 0x4e95, {0xa1, 0xd7, 0x5c, 0x8e, 0x2f, 0x0b, 0x6a, 0x43}};
const wchar_t DESCRIPTION[] = L"XVoice";
const wchar_t WINDOW_CLASS[] = L"XVoiceTipWindow";
const wchar_t PROTOCOL[] = L"1";
const DWORD MAX_TEXT_BYTES = 1 << 20; // xvoice.exe sends one dictation at most.
const LANGID LANGUAGES[] = {0x0804, 0x0409}; // zh-CN, en-US
const GUID CATEGORIES[] = {GUID_TFCAT_TIP_SPEECH, GUID_TFCAT_TIPCAP_IMMERSIVESUPPORT, GUID_TFCAT_TIPCAP_SYSTRAYSUPPORT};

enum Command : ULONG_PTR { QUERY = 1, COMPOSE = 2, COMMIT = 3 };

HINSTANCE dllInstance;

class TextService;

// One queued edit: COMPOSE or COMMIT of text into the service's context.
class EditSession final : public ITfEditSession {
public:
    EditSession(TextService *service, ITfContext *context, Command command, std::wstring text);
    ~EditSession();
    STDMETHODIMP QueryInterface(REFIID riid, void **ppv) override {
        if (riid == IID_IUnknown || riid == IID_ITfEditSession) {
            *ppv = static_cast<ITfEditSession *>(this);
            AddRef();
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&refs); }
    STDMETHODIMP_(ULONG) Release() override {
        LONG r = InterlockedDecrement(&refs);
        if (!r) delete this;
        return r;
    }
    STDMETHODIMP DoEditSession(TfEditCookie ec) override;

private:
    LONG refs = 1;
    TextService *service;
    ITfContext *context;
    Command command;
    std::wstring text;
};

class TextService final : public ITfTextInputProcessorEx, public ITfThreadMgrEventSink, public ITfCompositionSink {
public:
    STDMETHODIMP QueryInterface(REFIID riid, void **ppv) override {
        if (riid == IID_IUnknown || riid == IID_ITfTextInputProcessor || riid == IID_ITfTextInputProcessorEx) {
            *ppv = static_cast<ITfTextInputProcessorEx *>(this);
        } else if (riid == IID_ITfThreadMgrEventSink) {
            *ppv = static_cast<ITfThreadMgrEventSink *>(this);
        } else if (riid == IID_ITfCompositionSink) {
            *ppv = static_cast<ITfCompositionSink *>(this);
        } else {
            *ppv = nullptr;
            return E_NOINTERFACE;
        }
        AddRef();
        return S_OK;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return InterlockedIncrement(&refs); }
    STDMETHODIMP_(ULONG) Release() override {
        LONG r = InterlockedDecrement(&refs);
        if (!r) delete this;
        return r;
    }

    STDMETHODIMP Activate(ITfThreadMgr *threadMgr, TfClientId id) override { return ActivateEx(threadMgr, id, 0); }
    STDMETHODIMP ActivateEx(ITfThreadMgr *threadMgr, TfClientId id, DWORD) override {
        manager = threadMgr;
        manager->AddRef();
        client = id;
        ITfSource *source = nullptr;
        if (SUCCEEDED(manager->QueryInterface(IID_ITfSource, (void **)&source))) {
            source->AdviseSink(IID_ITfThreadMgrEventSink, static_cast<ITfThreadMgrEventSink *>(this), &sinkCookie);
            source->Release();
        }
        ITfDocumentMgr *focus = nullptr;
        if (SUCCEEDED(manager->GetFocus(&focus)) && focus) {
            EnsureWindow();
            focus->Release();
        }
        return S_OK;
    }
    STDMETHODIMP Deactivate() override {
        if (window) {
            DestroyWindow(window);
            window = nullptr;
        }
        DropComposition();
        if (manager) {
            ITfSource *source = nullptr;
            if (sinkCookie != TF_INVALID_COOKIE && SUCCEEDED(manager->QueryInterface(IID_ITfSource, (void **)&source))) {
                source->UnadviseSink(sinkCookie);
                source->Release();
            }
            sinkCookie = TF_INVALID_COOKIE;
            manager->Release();
            manager = nullptr;
        }
        return S_OK;
    }

    STDMETHODIMP OnInitDocumentMgr(ITfDocumentMgr *) override { return S_OK; }
    STDMETHODIMP OnUninitDocumentMgr(ITfDocumentMgr *) override { return S_OK; }
    STDMETHODIMP OnSetFocus(ITfDocumentMgr *focus, ITfDocumentMgr *) override {
        generation++;
        if (focus) EnsureWindow();
        return S_OK;
    }
    STDMETHODIMP OnPushContext(ITfContext *) override { return S_OK; }
    STDMETHODIMP OnPopContext(ITfContext *) override { return S_OK; }

    // The application or another text service ended our composition.
    STDMETHODIMP OnCompositionTerminated(TfEditCookie, ITfComposition *ended) override {
        if (ended == composition) {
            DropComposition();
            generation++;
        }
        return S_OK;
    }

    // Edit session helpers, only called inside DoEditSession.
    HRESULT Compose(TfEditCookie ec, ITfContext *context, const std::wstring &text) {
        if (composition && composedContext != context) EndComposition(ec);
        if (!composition) {
            TF_SELECTION selection = {};
            ULONG fetched = 0;
            HRESULT hr = context->GetSelection(ec, TF_DEFAULT_SELECTION, 1, &selection, &fetched);
            if (FAILED(hr) || !fetched || !selection.range) return FAILED(hr) ? hr : E_FAIL;
            ITfContextComposition *composer = nullptr;
            hr = context->QueryInterface(IID_ITfContextComposition, (void **)&composer);
            if (SUCCEEDED(hr)) {
                hr = composer->StartComposition(ec, selection.range, static_cast<ITfCompositionSink *>(this), &composition);
                composer->Release();
            }
            selection.range->Release();
            if (FAILED(hr) || !composition) {
                composition = nullptr;
                return FAILED(hr) ? hr : E_FAIL;
            }
            composedContext = context;
            composedContext->AddRef();
        }
        ITfRange *range = nullptr;
        HRESULT hr = composition->GetRange(&range);
        if (FAILED(hr)) return hr;
        hr = range->SetText(ec, 0, text.c_str(), (LONG)text.size());
        if (SUCCEEDED(hr)) {
            // Keep the caret after the composed text.
            range->Collapse(ec, TF_ANCHOR_END);
            TF_SELECTION caret = {range, {TF_AE_NONE, FALSE}};
            context->SetSelection(ec, 1, &caret);
        }
        range->Release();
        return hr;
    }
    HRESULT Commit(TfEditCookie ec, ITfContext *context, const std::wstring &text) {
        if (!text.empty()) {
            HRESULT hr = Compose(ec, context, text);
            if (FAILED(hr)) return hr;
        }
        EndComposition(ec);
        return S_OK;
    }

    LRESULT Handle(ULONG_PTR command, const std::wstring &text) {
        ITfContext *context = FocusedContext();
        if (command == QUERY) {
            if (!context) return 0;
            TF_STATUS status = {};
            context->GetStatus(&status);
            bool writable = !(status.dwDynamicFlags & TF_SD_READONLY) && !Flagged(context, GUID_COMPARTMENT_KEYBOARD_DISABLED) &&
                            !Flagged(context, GUID_COMPARTMENT_EMPTYCONTEXT);
            context->Release();
            return ((LRESULT)(generation & 0x1fffffff) << 2) | (writable ? 1 : 2);
        }
        if (!context) return 0;
        LRESULT queued = 0;
        if (command == COMPOSE || command == COMMIT) {
            EditSession *session = new (std::nothrow) EditSession(this, context, (Command)command, text);
            if (session) {
                HRESULT result = S_OK;
                // Always queued: never edit inside a message another process waits on.
                queued = SUCCEEDED(context->RequestEditSession(client, session, TF_ES_ASYNC | TF_ES_READWRITE, &result)) ? 1 : 0;
                session->Release();
            }
        }
        context->Release();
        return queued;
    }

    ~TextService() {
        if (window) DestroyWindow(window);
        DropComposition();
    }

private:
    LONG refs = 1;
    ITfThreadMgr *manager = nullptr;
    TfClientId client = TF_CLIENTID_NULL;
    DWORD sinkCookie = TF_INVALID_COOKIE;
    HWND window = nullptr;
    ITfComposition *composition = nullptr;
    ITfContext *composedContext = nullptr;
    ULONG generation = 1;

    ITfContext *FocusedContext() {
        ITfDocumentMgr *focus = nullptr;
        if (!manager || FAILED(manager->GetFocus(&focus)) || !focus) return nullptr;
        ITfContext *context = nullptr;
        focus->GetTop(&context);
        focus->Release();
        return context;
    }

    // Flagged reads a context compartment the application sets, such as
    // keyboard-disabled on password fields.
    static bool Flagged(ITfContext *context, REFGUID compartment) {
        ITfCompartmentMgr *compartments = nullptr;
        if (FAILED(context->QueryInterface(IID_ITfCompartmentMgr, (void **)&compartments))) return false;
        ITfCompartment *item = nullptr;
        VARIANT value;
        VariantInit(&value);
        if (SUCCEEDED(compartments->GetCompartment(compartment, &item))) {
            item->GetValue(&value);
            item->Release();
        }
        compartments->Release();
        bool set = value.vt == VT_I4 && value.lVal != 0;
        VariantClear(&value);
        return set;
    }

    void EndComposition(TfEditCookie ec) {
        if (composition) composition->EndComposition(ec);
        DropComposition();
    }
    void DropComposition() {
        if (composition) {
            composition->Release();
            composition = nullptr;
        }
        if (composedContext) {
            composedContext->Release();
            composedContext = nullptr;
        }
    }

    // The window is created on the first text focus so that threads that
    // never edit text (most of them) carry nothing.
    void EnsureWindow() {
        if (window) return;
        WNDCLASSEXW wc = {sizeof(wc)};
        wc.lpfnWndProc = WindowProc;
        wc.hInstance = dllInstance;
        wc.lpszClassName = WINDOW_CLASS;
        RegisterClassExW(&wc); // Fails harmlessly once registered.
        window = CreateWindowExW(0, WINDOW_CLASS, PROTOCOL, 0, 0, 0, 0, 0, HWND_MESSAGE, nullptr, dllInstance, nullptr);
        if (window) SetWindowLongPtrW(window, GWLP_USERDATA, (LONG_PTR)this);
    }

    static LRESULT CALLBACK WindowProc(HWND hwnd, UINT message, WPARAM wParam, LPARAM lParam) {
        if (message == WM_COPYDATA) {
            auto *service = (TextService *)GetWindowLongPtrW(hwnd, GWLP_USERDATA);
            auto *data = (const COPYDATASTRUCT *)lParam;
            if (!service || !data || data->cbData > MAX_TEXT_BYTES) return 0;
            try {
                std::wstring text;
                if (data->lpData && data->cbData >= sizeof(wchar_t)) text.assign((const wchar_t *)data->lpData, data->cbData / sizeof(wchar_t));
                return service->Handle(data->dwData, text);
            } catch (...) {
                return 0; // Out of memory: refuse the command, never take the application down.
            }
        }
        return DefWindowProcW(hwnd, message, wParam, lParam);
    }
};

EditSession::EditSession(TextService *s, ITfContext *c, Command cmd, std::wstring t) : service(s), context(c), command(cmd), text(std::move(t)) {
    service->AddRef();
    context->AddRef();
}

EditSession::~EditSession() {
    context->Release();
    service->Release();
}

STDMETHODIMP EditSession::DoEditSession(TfEditCookie ec) {
    return command == COMPOSE ? service->Compose(ec, context, text) : service->Commit(ec, context, text);
}

class Factory final : public IClassFactory {
public:
    STDMETHODIMP QueryInterface(REFIID riid, void **ppv) override {
        if (riid == IID_IUnknown || riid == IID_IClassFactory) {
            *ppv = static_cast<IClassFactory *>(this);
            return S_OK;
        }
        *ppv = nullptr;
        return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() override { return 2; }
    STDMETHODIMP_(ULONG) Release() override { return 1; }
    STDMETHODIMP CreateInstance(IUnknown *outer, REFIID riid, void **ppv) override {
        *ppv = nullptr;
        if (outer) return CLASS_E_NOAGGREGATION;
        TextService *service = new (std::nothrow) TextService();
        if (!service) return E_OUTOFMEMORY;
        HRESULT hr = service->QueryInterface(riid, ppv);
        service->Release();
        return hr;
    }
    STDMETHODIMP LockServer(BOOL) override { return S_OK; }
};

Factory factory;

std::wstring ClsidText() {
    wchar_t clsid[40];
    StringFromGUID2(CLSID_XVoiceTip, clsid, 40);
    return clsid;
}

std::wstring ClsidKey() { return L"Software\\Classes\\CLSID\\" + ClsidText(); }

} // namespace

BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID) {
    if (reason == DLL_PROCESS_ATTACH) {
        dllInstance = instance;
        DisableThreadLibraryCalls(instance);
    }
    return TRUE;
}

STDAPI DllGetClassObject(REFCLSID clsid, REFIID riid, void **ppv) {
    if (clsid != CLSID_XVoiceTip) {
        *ppv = nullptr;
        return CLASS_E_CLASSNOTAVAILABLE;
    }
    return factory.QueryInterface(riid, ppv);
}

// Windows and edit sessions point into this DLL; it stays until the process exits.
STDAPI DllCanUnloadNow() { return S_FALSE; }

STDAPI DllUnregisterServer();

// Machine-wide registration (regsvr32, elevated): the COM class, one profile
// per language and the speech category that makes TSF activate it alongside
// the keyboard IME.
STDAPI DllRegisterServer() {
    wchar_t path[MAX_PATH];
    DWORD length = GetModuleFileNameW(dllInstance, path, MAX_PATH);
    if (!length || length >= MAX_PATH) return SELFREG_E_CLASS;
    HKEY key = nullptr, server = nullptr;
    if (RegCreateKeyExW(HKEY_LOCAL_MACHINE, ClsidKey().c_str(), 0, nullptr, 0, KEY_WRITE, nullptr, &key, nullptr) != ERROR_SUCCESS) return SELFREG_E_CLASS;
    RegSetValueExW(key, nullptr, 0, REG_SZ, (const BYTE *)DESCRIPTION, sizeof(DESCRIPTION));
    LONG r = RegCreateKeyExW(key, L"InprocServer32", 0, nullptr, 0, KEY_WRITE, nullptr, &server, nullptr);
    if (r == ERROR_SUCCESS) {
        RegSetValueExW(server, nullptr, 0, REG_SZ, (const BYTE *)path, (length + 1) * sizeof(wchar_t));
        RegSetValueExW(server, L"ThreadingModel", 0, REG_SZ, (const BYTE *)L"Apartment", sizeof(L"Apartment"));
        RegCloseKey(server);
    }
    RegCloseKey(key);
    if (r != ERROR_SUCCESS) return SELFREG_E_CLASS;

    ITfInputProcessorProfileMgr *profiles = nullptr;
    ITfCategoryMgr *categories = nullptr;
    HRESULT hr = CoCreateInstance(CLSID_TF_InputProcessorProfiles, nullptr, CLSCTX_INPROC_SERVER, IID_ITfInputProcessorProfileMgr, (void **)&profiles);
    if (SUCCEEDED(hr)) hr = CoCreateInstance(CLSID_TF_CategoryMgr, nullptr, CLSCTX_INPROC_SERVER, IID_ITfCategoryMgr, (void **)&categories);
    for (LANGID language : LANGUAGES) {
        if (SUCCEEDED(hr)) hr = profiles->RegisterProfile(CLSID_XVoiceTip, language, PROFILE_XVoiceTip, DESCRIPTION, (ULONG)wcslen(DESCRIPTION), path, length, 0, nullptr, 0, TRUE, 0);
    }
    for (const GUID &category : CATEGORIES) {
        if (SUCCEEDED(hr)) hr = categories->RegisterCategory(CLSID_XVoiceTip, category, CLSID_XVoiceTip);
    }
    if (categories) categories->Release();
    if (profiles) profiles->Release();
    if (FAILED(hr)) DllUnregisterServer();
    return hr;
}

STDAPI DllUnregisterServer() {
    ITfInputProcessorProfileMgr *profiles = nullptr;
    ITfCategoryMgr *categories = nullptr;
    if (SUCCEEDED(CoCreateInstance(CLSID_TF_CategoryMgr, nullptr, CLSCTX_INPROC_SERVER, IID_ITfCategoryMgr, (void **)&categories))) {
        for (const GUID &category : CATEGORIES) categories->UnregisterCategory(CLSID_XVoiceTip, category, CLSID_XVoiceTip);
        categories->Release();
    }
    if (SUCCEEDED(CoCreateInstance(CLSID_TF_InputProcessorProfiles, nullptr, CLSCTX_INPROC_SERVER, IID_ITfInputProcessorProfileMgr, (void **)&profiles))) {
        for (LANGID language : LANGUAGES) profiles->UnregisterProfile(CLSID_XVoiceTip, language, PROFILE_XVoiceTip, 0);
        profiles->Release();
    }
    RegDeleteTreeW(HKEY_LOCAL_MACHINE, ClsidKey().c_str());
    // UnregisterProfile leaves the empty TIP key behind.
    RegDeleteTreeW(HKEY_LOCAL_MACHINE, (L"Software\\Microsoft\\CTF\\TIP\\" + ClsidText()).c_str());
    return S_OK;
}
