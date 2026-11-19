window.__ModuleLoader__.load({
  id: 'dsh-plugin-oneclickvirt',
  factory(require) {
    const React = require('react'), h = React.createElement
    const releaseVersion = /* OCV_RELEASE */ 'v20261119-104512'
    const dictionaries = /* OCV_LOCALES */ {"en":{"meta":{"title":"OneClickVirt","description":"Manage VMs, containers and nodes from DeepSeek Harness."},"panel":"OneClickVirt","subtitle":"Your virtual infrastructure, within reach","overview":"Overview","instances":"Instances","create":"Create instance","providers":"Nodes","tasks":"Tasks","settings":"Plugin settings","notConnected":"Not signed in","login":"Sign in","logout":"Sign out","address":"Deployment URL","loginMode":"Sign-in method","passwordLogin":"Username and password","tokenLogin":"API token","username":"Username","password":"Password","apiToken":"API token","captcha":"Load / refresh captcha","captchaCode":"Captcha (if required)","remember":"Remember sign-in","rememberHint":"Password sign-in creates a permanent API token for this device and revokes it on sign-out. An existing token is saved without changing its expiry and is not revoked. Passwords are never saved. If the account token limit is reached, use an existing token or turn this off.","allowHTTP":"Allow unencrypted HTTP for this deployment (credentials travel without encryption)","revokeWarning":"Local sign-in was cleared, but the device token could not be revoked. Remove the DSH token in the platform Profile → API Tokens.","retryLogin":"The saved login could not be verified. Check connectivity and retry.","retry":"Retry connection","loading":"Loading…","working":"Working…","previous":"Previous","next":"Next","empty":"Nothing to display yet","saved":"Saved","submitted":"Submitted. Check Tasks and the instance state to confirm completion.","selectImage":"Choose an image from the selected node catalog.","name":"Name","cpu":"CPU cores","memory":"Memory (MB)","disk":"Disk (GB)","bandwidth":"Bandwidth (Mbps)","user_id":"Owner user ID (optional)","description":"Description","search":"Search original name","originalName":"Original instance name","status":"Status","refresh":"Refresh","resources":"Resources","actions":"Actions","start":"Start","stop":"Stop","restart":"Restart","alias":"Display alias","delete":"Delete","provider":"Node","instanceType":"Instance type","container":"Container","vm":"Virtual machine","image":"Image","network":"Network type","createHint":"Images and specification IDs come from the selected node. Quotas, availability and permissions are still checked by OneClickVirt.","defaultNetwork":"Leave network type blank to use the platform default.","health":"Check health","metrics":"Monitoring","task":"Task","progress":"Progress","details":"Details","sidebar":"Show OneClickVirt in the sidebar","sidebarHint":"When hidden, reopen it from the sidebar More menu.","persistent":"Sign-in is saved on the DSH host","temporary":"Session-only sign-in","hostHint":"Credentials stay on the DSH host, in its credentials provider or a private plugin file. Browser storage contains only UI preferences and creation choices. Sign out before uninstalling to clear the saved login.","headless":"CLI / CI uses the same native tools. Set the two environment variables below on the DSH host; never paste credentials into chat.","version":"Plugin version","close":"Close","cancel":"Cancel","confirm":"Confirm operation","aliasHint":"Up to 128 characters. Clear to display the original name. Search and host identifiers keep using the original name.","deleteHint":"This permanently deletes the instance. Enter its exact original name to confirm.","diagnostics":"Diagnostics","logsHint":"The current API provides instance state and task errors; it does not expose guest logs.","restore":"Open OneClickVirt","taskHint":"Status refreshes every 10 seconds while this page is visible. An accepted task can still fail."},"zh":{"meta":{"title":"OneClickVirt","description":"在 DeepSeek Harness 中创建和管理虚拟机、容器与节点。"},"panel":"OneClickVirt","subtitle":"在 DSH 中管理虚拟机与容器","overview":"概览","instances":"实例","create":"创建实例","providers":"节点","tasks":"任务","settings":"插件设置","notConnected":"未登录","login":"登录","logout":"退出登录","address":"平台地址","loginMode":"登录方式","passwordLogin":"账号密码","tokenLogin":"API Token","username":"用户名","password":"密码","apiToken":"API Token","captcha":"加载／刷新验证码","captchaCode":"验证码（按需填写）","remember":"保持登录","rememberHint":"账号密码登录时，为本设备创建独立的长期 API Token，退出时撤销；已有 Token 沿用其有效期，退出不撤销。密码不保存。达到账号 Token 数量限制时，可使用已有 Token 或关闭保持登录。","allowHTTP":"允许此平台使用明文 HTTP（凭据将以明文传输）","revokeWarning":"已清除本地登录，但设备 Token 撤销失败，请到平台“个人中心 → API Token”删除对应的 DSH Token。","retryLogin":"暂时无法验证已保存的登录，请检查连接后重试。","retry":"重试连接","loading":"正在读取…","working":"处理中…","previous":"上一页","next":"下一页","empty":"暂无数据","saved":"已保存","submitted":"已提交，请查看任务和实例状态确认是否完成。","selectImage":"请从当前节点的镜像列表选择镜像。","name":"名称","cpu":"CPU 核心数","memory":"内存（MB）","disk":"硬盘（GB）","bandwidth":"带宽（Mbps）","user_id":"所属用户 ID（可选）","description":"说明","search":"搜索实例原名","originalName":"实例原名","status":"状态","refresh":"刷新","resources":"配置","actions":"操作","start":"开机","stop":"关机","restart":"重启","alias":"备注别名","delete":"删除","provider":"节点","instanceType":"实例类型","container":"容器","vm":"虚拟机","image":"镜像","network":"网络类型","createHint":"镜像和规格 ID 从所选节点读取，配额、可用性与权限仍由 OneClickVirt 校验。","defaultNetwork":"网络类型留空时沿用平台默认设置。","health":"健康检查","metrics":"资源监控","task":"任务","progress":"进度","details":"详情","sidebar":"在左侧栏显示 OneClickVirt","sidebarHint":"隐藏后可从左侧栏“更多”重新打开。","persistent":"登录已保存在 DSH 主机","temporary":"仅本次登录","hostHint":"凭据保存在 DSH 主机的凭据服务或插件私有文件中；浏览器只保存界面偏好和创建选项。卸载前请先退出登录以清除记录。","headless":"CLI／CI 可使用同一套原生工具。在运行 DSH 的主机上配置下列环境变量，请勿把凭据发到对话中。","version":"插件版本","close":"关闭","cancel":"取消","confirm":"确认操作","aliasHint":"最多 128 个字符，留空恢复原名显示；搜索与宿主机标识仍使用原名。","deleteHint":"此操作将永久删除实例。请输入完整实例原名确认。","diagnostics":"诊断","logsHint":"当前 API 可读取实例状态与任务错误，暂不提供客户机内部日志。","restore":"打开 OneClickVirt","taskHint":"页面显示时每 10 秒刷新状态；任务受理后仍可能失败，请确认最终状态。"}}
    const preferenceKey = 'oneclickvirt.dsh.ui.v1', saved = { tab: 'overview', sidebar: true, address: '', drafts: {} }
    try { const value = JSON.parse(localStorage.getItem(preferenceKey) || '{}'); for (const key of Object.keys(saved)) if (typeof value[key] === typeof saved[key]) saved[key] = value[key] } catch {}
    const persist = () => { try { localStorage.setItem(preferenceKey, JSON.stringify(saved)) } catch {} }
    let updateSidebar = () => {}, disposed = false, seat = null
    const observers = new Set(), notifySeat = value => { seat = value; for (const fn of observers) fn() }
    const subscribeSeat = fn => { observers.add(fn); return () => observers.delete(fn) }
    async function api(input, signal) {
      const response = await fetch('/api/oneclickvirt.plugin', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input), signal })
      const result = await response.json()
      if (!response.ok) { const error = new Error(result.error?.message || 'OneClickVirt request failed'); error.code = result.error?.code; throw error }
      return result.data
    }
    const rows = data => Array.isArray(data) ? data : Array.isArray(data?.list) ? data.list : []
    const original = row => row.name || row.instanceName || String(row.id)
    const display = row => row.alias || original(row)
    const field = (label, value, set, options = {}) => h('label', { key: options.name || label }, label, h('input', { 'aria-label': label, value: value ?? '', onChange: e => set(e.target.value), ...options }))
    const select = (label, value, set, items, disabled = false) => h('label', { key: label }, label, h('select', { 'aria-label': label, value: value ?? '', onChange: e => set(e.target.value), disabled }, h('option', { value: '' }, '—'), ...items.map(([id, text]) => h('option', { key: id, value: id }, text))))
    function Icon() { return h('svg', { width: 20, height: 20, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.7, 'aria-hidden': true }, h('rect', { x: 3, y: 3, width: 18, height: 7, rx: 2 }), h('rect', { x: 3, y: 14, width: 18, height: 7, rx: 2 }), h('path', { d: 'M7 6.5h1m3 0h6M7 17.5h1m3 0h6' })) }
    function Seat() {
      const ref = React.useRef(null)
      React.useLayoutEffect(() => {
        const measure = () => { const rect = ref.current?.getBoundingClientRect(); if (rect) notifySeat({ top: rect.top, left: rect.left, width: rect.width, height: rect.height }) }
        const resize = new ResizeObserver(measure); resize.observe(ref.current); window.addEventListener('resize', measure); measure()
        return () => { resize.disconnect(); window.removeEventListener('resize', measure); notifySeat(null) }
      }, [])
      return h('div', { ref, style: { height: '100%', minHeight: 0 } })
    }
    function Keeper({ t }) {
      const rect = React.useSyncExternalStore(subscribeSeat, () => seat), [started, start] = React.useState(Boolean(rect))
      React.useEffect(() => { if (rect) start(true) }, [Boolean(rect)])
      if (!started && !rect) return null
      return h('div', { 'data-oneclickvirt-keeper': true, hidden: !rect, style: { position: 'fixed', ...(rect || {}), display: rect ? 'block' : 'none', zIndex: 0, pointerEvents: rect ? 'auto' : 'none' } }, h(Panel, { t, visible: Boolean(rect) }))
    }
    const css = `.ocv{height:100%;overflow:auto;background:var(--dsw-alias-bg-base,#f6f8fc);color:var(--dsw-alias-label-primary,#18283d);font:14px/1.6 system-ui,sans-serif;padding:calc(28px + var(--dsh-frame-top-clearance,0px)) 28px 36px;box-sizing:border-box}.ocv *{box-sizing:border-box}.ocv-inner{max-width:1240px;margin:auto}.ocv header{display:flex;align-items:center;justify-content:space-between;gap:14px;margin-bottom:24px;flex-wrap:wrap}.ocv-brand{display:flex;align-items:center;gap:13px}.ocv-mark{background:#315cdb;color:white;border-radius:14px;padding:12px;display:grid;place-items:center}.ocv h1{font-size:25px;letter-spacing:-.5px;line-height:1.3;margin:0}.ocv h2{font-size:17px;margin:0 0 16px}.ocv small,.ocv-muted{color:var(--dsw-alias-label-secondary,#697b92)}.ocv nav{display:flex;gap:5px;overflow:auto;margin:0 0 24px;padding:4px;border:1px solid var(--dsw-alias-border-l2,#dee5ef);border-radius:12px;width:max-content;max-width:100%}.ocv button,.ocv input,.ocv select,.ocv textarea{font:inherit;color:inherit;background:var(--dsw-alias-bg-layer-1,#fff);border:1px solid var(--dsw-alias-border-l2,#dce4ef);border-radius:8px;padding:9px 12px;max-width:100%}.ocv button{cursor:pointer;white-space:nowrap;line-height:1.4}.ocv button:hover{background:var(--dsw-alias-interactive-bg-hover,#edf2ff)}.ocv button:disabled{opacity:.45;cursor:default}.ocv .ocv-primary,.ocv nav [aria-selected=true]{background:#315cdb;color:white;border-color:#315cdb}.ocv .ocv-danger{color:#d23d51}.ocv :focus-visible{outline:2px solid #6488ef;outline-offset:3px}.ocv-card{padding:22px;border:1px solid var(--dsw-alias-border-l2,#e1e7f0);background:var(--dsw-alias-bg-layer-1,#fff);border-radius:15px;margin-bottom:18px;min-width:0}.ocv-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.ocv-stack{display:flex;flex-direction:column;gap:14px}.ocv-row{display:flex;align-items:center;gap:9px;flex-wrap:wrap}.ocv-between{justify-content:space-between}.ocv label{display:flex;flex-direction:column;gap:6px;font-size:13px}.ocv label input,.ocv label select{width:100%}.ocv .ocv-check{flex-direction:row;align-items:center;gap:9px}.ocv-check input{width:auto!important;accent-color:#315cdb}.ocv-actions{display:flex;gap:8px;flex-wrap:wrap;margin-top:18px}.ocv-help{font-size:12px;color:var(--dsw-alias-label-secondary,#697b92);margin:9px 0}.ocv-error,.ocv-toast{padding:12px 16px;border-radius:10px;margin-bottom:18px;white-space:pre-wrap;overflow-wrap:anywhere}.ocv-error{background:#e44e5f12;color:var(--dsw-alias-state-error-primary,#b82740)}.ocv-toast{background:#315cdb12;color:var(--dsw-alias-label-primary,#25499b)}.ocv-table{overflow:auto}.ocv table{width:100%;border-collapse:collapse;font-size:13px}.ocv th,.ocv td{text-align:left;padding:12px 10px;vertical-align:top;border-bottom:1px solid var(--dsw-alias-border-l2,#e8edf4)}.ocv td small{display:block;overflow-wrap:anywhere}.ocv pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.7 ui-monospace,monospace;background:var(--dsw-alias-bg-base,#f7f9fc);padding:16px;border-radius:9px;max-height:480px;overflow:auto;margin:12px 0 0}.ocv-badge{font-size:12px;padding:4px 9px;border-radius:20px;background:#315cdb12;display:inline-block}.ocv-empty{padding:30px;text-align:center;color:var(--dsw-alias-label-secondary,#697b92)}.ocv-modal-backdrop{position:fixed;inset:0;z-index:200;background:#0a173c66;display:grid;place-items:center;padding:20px}.ocv-modal{width:min(540px,100%);max-height:85vh;overflow:auto;background:var(--dsw-alias-bg-layer-1,#fff);border-radius:16px;padding:26px;box-shadow:0 20px 80px #0003}.ocv a{color:#315cdb;overflow-wrap:anywhere}.ocv-captcha{max-height:70px;max-width:220px}.ocv-menu{border:0;background:none;color:inherit;padding:8px 12px;cursor:pointer;width:100%;text-align:left}@media(max-width:800px){.ocv{padding:calc(18px + var(--dsh-frame-top-clearance,0px)) 15px 24px}.ocv-grid{grid-template-columns:1fr}.ocv th,.ocv td{padding:9px 7px}}`
    function Panel({ t, visible }) {
      const [status, setStatus] = React.useState(null), [tab, setTab] = React.useState(saved.tab)
      const [login, setLogin] = React.useState({ baseUrl: saved.address, mode: 'password', username: '', password: '', token: '', remember: true, allowHTTP: false, captchaId: '', captcha: '' })
      const [captcha, setCaptcha] = React.useState(''), [busy, setBusy] = React.useState(false), [error, setError] = React.useState(''), [toast, setToast] = React.useState('')
      const [instances, setInstances] = React.useState({ list: [], total: 0 }), [providers, setProviders] = React.useState([]), [tasks, setTasks] = React.useState({ list: [], total: 0 }), [overview, setOverview] = React.useState(null)
      const [refreshKey, setRefreshKey] = React.useState(0)
      const [search, setSearch] = React.useState(''), [filter, setFilter] = React.useState(''), [page, setPage] = React.useState(1), [providerPage, setProviderPage] = React.useState(1), [providerTotal, setProviderTotal] = React.useState(0), [taskPage, setTaskPage] = React.useState(1)
      const [detail, setDetail] = React.useState(null), [modal, setModal] = React.useState(null), [alias, setAlias] = React.useState(''), [confirmation, setConfirmation] = React.useState('')
      const [draft, setDraft] = React.useState({}), [catalog, setCatalog] = React.useState(null), [catalogBusy, setCatalogBusy] = React.useState(false), [sidebar, setSidebar] = React.useState(saved.sidebar)
      const lifetime = React.useRef(null), epoch = React.useRef(0), account = React.useRef(''), locked = React.useRef(false), signingOut = React.useRef(false), catalogRevision = React.useRef(0)
      const connected = Boolean(status?.connected), admin = Boolean(status?.admin)
      const scopeOf = state => state?.connected ? state.baseUrl + '/' + state.user.id : ''
      const wipe = () => { setInstances({ list: [], total: 0 }); setProviders([]); setTasks({ list: [], total: 0 }); setOverview(null); setDetail(null); setModal(null); setCatalog(null); setDraft({}); setPage(1); setTaskPage(1); setProviderPage(1); setProviderTotal(0); setSearch(''); setFilter('') }
      const adopt = data => {
        const scope = scopeOf(data)
        if (scope !== account.current) {
          ++epoch.current; ++catalogRevision.current; account.current = scope; wipe(); setToast('')
          if (scope) setDraft(saved.drafts[scope] || { instance_type: 'container', cpu: '1', memory: '512', disk: '10' })
        }
        setStatus(data)
        if (data.baseUrl) setLogin(value => ({ ...value, baseUrl: data.baseUrl }))
        return data
      }
      const call = (name, args = {}, signal = lifetime.current?.signal) => api({ action: 'call', name, args }, signal)
      const refreshStatus = async signal => { const revision = epoch.current, data = await api({ action: 'status' }, signal); if (!signal?.aborted && !signingOut.current && revision === epoch.current) adopt(data); return data }
      const execute = async work => {
        if (locked.current) return
        locked.current = true; setBusy(true); setError(''); setToast('')
        const revision = epoch.current
        try { return await work() }
        catch (e) {
          if (!lifetime.current?.signal.aborted && revision === epoch.current) {
            setError(e.message)
            if (['UNAUTHORIZED', 'LOGIN_REQUIRED', 'SESSION_CHANGED'].includes(e.code)) await refreshStatus(lifetime.current?.signal).catch(() => {})
          }
        } finally { locked.current = false; setBusy(false) }
      }
      React.useEffect(() => {
        const controller = new AbortController(); lifetime.current = controller
        refreshStatus(controller.signal).catch(e => { if (!controller.signal.aborted) setError(e.message) })
        return () => { ++epoch.current; ++catalogRevision.current; controller.abort() }
      }, [])
      React.useEffect(() => { saved.tab = tab; persist() }, [tab])
      React.useEffect(() => {
        if (!account.current || !draft.instance_type) return
        // Only creation choices are persisted in browser storage. Credentials
        // and account data are never copied into this preference record.
        saved.drafts[account.current] = draft
        const keys = Object.keys(saved.drafts); if (keys.length > 12) delete saved.drafts[keys[0]]
        persist()
      }, [draft])
      React.useEffect(() => {
        if (!visible) return
        const controller = new AbortController()
        refreshStatus(controller.signal).catch(e => { if (!controller.signal.aborted) setError(e.message) })
        return () => controller.abort()
      }, [visible])
      React.useEffect(() => {
        if (!connected || !visible) return
        const controller = new AbortController(), revision = epoch.current
        let running = false
        const load = async () => {
          if (running || signingOut.current) return
          running = true
          try {
            const name = ({ overview: 'get_system_status', instances: 'list_instances', providers: 'list_providers', create: 'list_providers', tasks: 'tasks' })[tab]
            if (!name) return
            const data = await call(name, tab === 'overview' ? {} : tab === 'instances' ? { page, pageSize: 20, name: search, status: filter } : { page: tab === 'tasks' ? taskPage : providerPage, pageSize: 20 }, controller.signal)
            if (controller.signal.aborted || revision !== epoch.current) return
            if (tab === 'overview') setOverview(data)
            else if (tab === 'instances') setInstances({ list: rows(data), total: data?.total ?? rows(data).length })
            else if (tab === 'tasks') setTasks({ list: rows(data), total: data?.total ?? rows(data).length })
            else { setProviders(rows(data)); setProviderTotal(data?.total ?? rows(data).length) }
          } catch (e) {
            if (!controller.signal.aborted && revision === epoch.current) {
              setError(e.message)
              if (['UNAUTHORIZED', 'LOGIN_REQUIRED', 'SESSION_CHANGED'].includes(e.code)) await refreshStatus(controller.signal).catch(() => {})
            }
          } finally { running = false }
        }
        load(); const timer = setInterval(load, 10000)
        return () => { controller.abort(); clearInterval(timer) }
      }, [connected, status?.baseUrl, status?.user?.id, admin, visible, tab, page, taskPage, providerPage, search, filter, toast, refreshKey])
      React.useEffect(() => {
        if (!connected || !visible || tab !== 'create' || !draft.provider_id) return
        const controller = new AbortController(), revision = ++catalogRevision.current, currentEpoch = epoch.current
        setCatalog(null); setCatalogBusy(true)
        call('catalog', { provider_id: Number(draft.provider_id), instance_type: draft.instance_type || 'container' }, controller.signal).then(value => {
          if (!controller.signal.aborted && revision === catalogRevision.current && currentEpoch === epoch.current) setCatalog(value)
        }).catch(e => { if (!controller.signal.aborted && revision === catalogRevision.current && currentEpoch === epoch.current) setError(e.message) }).finally(() => { if (revision === catalogRevision.current) setCatalogBusy(false) })
        return () => controller.abort()
      }, [connected, visible, tab, draft.provider_id, draft.instance_type])
      const button = (text, onClick, options = {}) => h('button', { type: 'button', disabled: busy, onClick, ...options }, text)
      const check = (text, checked, set) => h('label', { className: 'ocv-check' }, h('input', { type: 'checkbox', checked, onChange: e => set(e.target.checked) }), text)
      const json = data => h('pre', { tabIndex: 0 }, JSON.stringify(data, null, 2))
      const pager = (current, set, total) => h('div', { className: 'ocv-actions' }, button(t('previous'), () => set(current - 1), { disabled: busy || current <= 1 }), h('span', null, current + ' / ' + Math.max(1, Math.ceil(total / 20))), button(t('next'), () => set(current + 1), { disabled: busy || current * 20 >= total }))
      const table = (headers, content) => h('div', { className: 'ocv-table' }, content.length ? h('table', null, h('thead', null, h('tr', null, ...headers.map(text => h('th', { key: text, scope: 'col' }, text)))), h('tbody', null, ...content)) : h('p', { className: 'ocv-empty' }, t('empty')))
      const updateDraft = (key, value) => { if (String(draft[key] ?? '') === String(value)) return; if (key === 'provider_id' || key === 'instance_type') { ++catalogRevision.current; setCatalog(null); setDraft(old => ({ ...old, [key]: value, image_id: '', cpu_id: '', memory_id: '', disk_id: '', bandwidth_id: '' })) } else setDraft(old => ({ ...old, [key]: value })) }
      const signIn = event => {
        event.preventDefault()
        execute(async () => {
          const result = await api({ action: 'login', ...login }, lifetime.current.signal)
          // Canonical URL only. Never store user-entered credentials in a URL.
          saved.address = result.baseUrl; persist()
          setLogin(old => ({ ...old, password: '', token: '', captcha: '', captchaId: '' })); setCaptcha('')
          adopt(result); setTab('overview')
        })
      }
      const signOut = () => execute(async () => {
        signingOut.current = true; ++epoch.current; ++catalogRevision.current
        try {
          const result = await api({ action: 'logout' }, lifetime.current.signal)
          adopt(result); setLogin(old => ({ ...old, password: '', token: '', captcha: '', captchaId: '' })); setCaptcha('')
          setError(result.remoteTokenRevoked ? '' : t('revokeWarning'))
        } catch (e) { setError(e.message) }
        finally { signingOut.current = false }
      })
      const inspect = row => execute(async () => { const revision = epoch.current, data = await call('get_instance_detail', { instance_id: row.id }); if (revision === epoch.current) setDetail({ id: row.id, data }) })
      const doMutation = () => execute(async () => {
        const chosen = modal, revision = epoch.current
        if (chosen.action === 'delete_instance' && confirmation !== original(chosen.row)) return
        await call(chosen.action, { instance_id: chosen.row.id, ...(chosen.action === 'delete_instance' ? { confirm: true } : chosen.action === 'instance_alias' ? { alias } : {}) })
        if (revision !== epoch.current) return
        if (chosen.action === 'instance_alias') {
          setInstances(old => ({ ...old, list: old.list.map(row => row.id === chosen.row.id ? { ...row, alias } : row) }))
          if (detail?.id === chosen.row.id) setDetail(old => ({ ...old, data: { ...old.data, alias } }))
          setToast(t('saved'))
        } else setToast(t('submitted'))
        setModal(null)
      })
      const create = event => {
        event.preventDefault()
        execute(async () => {
          const images = rows(catalog?.images), image = images.find(row => String(row.id) === draft.image_id)
          if (!image) throw new Error(t('selectImage'))
          const args = { provider_id: Number(draft.provider_id), instance_type: draft.instance_type }
          if (draft.network_type) args.network_type = draft.network_type
          if (admin) Object.assign(args, { image: image.name, cpu: Number(draft.cpu), memory: Number(draft.memory), disk: Number(draft.disk), ...(draft.name ? { name: draft.name } : {}), ...(draft.user_id ? { user_id: Number(draft.user_id) } : {}), ...(draft.bandwidth ? { bandwidth: Number(draft.bandwidth) } : {}) })
          else Object.assign(args, { image_id: image.id, cpu_id: draft.cpu_id, memory_id: draft.memory_id, disk_id: draft.disk_id, bandwidth_id: draft.bandwidth_id, description: draft.description || '' })
          const revision = epoch.current
          await call('create_instance', args)
          if (revision === epoch.current) { setToast(t('submitted')); setTab('tasks'); setTaskPage(1) }
        })
      }
      const selectedProvider = providers.find(row => String(row.id) === draft.provider_id)
      const networks = selectedProvider?.allowedNetworkTypes?.length ? selectedProvider.allowedNetworkTypes : ['nat_ipv4', 'nat_ipv4_ipv6', 'dedicated_ipv4', 'dedicated_ipv4_ipv6', 'ipv6_only', 'no_port_mapping']
      const creationFields = admin ? h('div', { className: 'ocv-grid' }, ...['name', 'cpu', 'memory', 'disk', 'bandwidth', 'user_id'].map(key => field(t(key), draft[key], value => updateDraft(key, value), { name: key, type: ['name'].includes(key) ? 'text' : 'number', min: key === 'name' ? undefined : 1, required: ['cpu', 'memory', 'disk'].includes(key) }))) : h('div', { className: 'ocv-grid' }, ...['cpu', 'memory', 'disk', 'bandwidth'].map(key => select(t(key), draft[key + '_id'], value => updateDraft(key + '_id', value), (catalog?.configuration?.[key === 'cpu' ? 'cpuSpecs' : key + 'Specs'] || []).map(row => [row.id, row.name]), catalogBusy)), field(t('description'), draft.description, value => updateDraft('description', value), { maxLength: 500 }))
      return h('div', { className: 'ocv' }, h('style', null, css), h('div', { className: 'ocv-inner' },
        h('header', null, h('div', { className: 'ocv-brand' }, h('div', { className: 'ocv-mark' }, h(Icon)), h('div', null, h('h1', null, 'OneClickVirt'), h('small', null, t('subtitle')))), h('div', { className: 'ocv-row' }, h('span', { className: 'ocv-badge' }, connected ? status.user.username + ' · ' + status.user.userType : t('notConnected')), connected && button(t('logout'), signOut))),
        h('nav', { role: 'tablist', 'aria-label': t('panel') }, ...['overview', 'instances', 'create', 'providers', 'tasks', 'settings'].map(name => button(t(name), () => setTab(name), { key: name, role: 'tab', 'aria-selected': tab === name, disabled: !connected && name !== 'settings' }))),
        error && h('div', { className: 'ocv-error', role: 'alert' }, error), toast && h('div', { className: 'ocv-toast', role: 'status' }, toast),
        status?.connectionError && h('p', { className: 'ocv-error' }, t('retryLogin'), ' ', button(t('retry'), () => execute(() => refreshStatus(lifetime.current.signal)))),
        status === null && h('p', { className: 'ocv-empty' }, t('loading')),
        !connected && h('section', { className: 'ocv-card' }, h('h2', null, t('login')), h('form', { onSubmit: signIn, className: 'ocv-stack' },
          field(t('address'), login.baseUrl, value => setLogin({ ...login, baseUrl: value, captchaId: '', captcha: '' }), { type: 'url', required: true, placeholder: 'https://virt.example.com', autoComplete: 'url' }),
          select(t('loginMode'), login.mode, value => { if (value) setLogin({ ...login, mode: value, password: '', token: '' }) }, [['password', t('passwordLogin')], ['token', t('tokenLogin')]]),
          login.mode === 'password' ? h(React.Fragment, null, field(t('username'), login.username, value => setLogin({ ...login, username: value }), { autoComplete: 'username', required: true }), field(t('password'), login.password, value => setLogin({ ...login, password: value }), { type: 'password', autoComplete: 'current-password', required: true }), h('div', { className: 'ocv-row' }, button(t('captcha'), () => execute(async () => { const value = await api({ action: 'captcha', baseUrl: login.baseUrl, allowHTTP: login.allowHTTP }, lifetime.current.signal); setCaptcha(value.image); setLogin(old => ({ ...old, captchaId: value.captchaId, captcha: '' })) })), captcha && h('img', { src: captcha, alt: t('captcha'), className: 'ocv-captcha' }), field(t('captchaCode'), login.captcha, value => setLogin({ ...login, captcha: value }), { autoComplete: 'off' }))) : field(t('apiToken'), login.token, value => setLogin({ ...login, token: value }), { type: 'password', autoComplete: 'off', required: true }),
          check(t('remember'), login.remember, value => setLogin({ ...login, remember: value })), h('p', { className: 'ocv-help' }, t('rememberHint')), check(t('allowHTTP'), login.allowHTTP, value => setLogin({ ...login, allowHTTP: value })), h('button', { className: 'ocv-primary', type: 'submit', disabled: busy }, t(busy ? 'working' : 'login')))),
        connected && tab === 'overview' && h('section', { className: 'ocv-card' }, h('div', { className: 'ocv-row ocv-between' }, h('h2', null, t('overview')), button(t('health'), () => execute(async () => { const revision = epoch.current, data = await call('resource', { uri: 'oneclickvirt://health/status' }); if (revision === epoch.current) setDetail({ data }) }))), h('p', { className: 'ocv-help' }, status.baseUrl), overview ? json(overview) : t('loading')),
        connected && tab === 'instances' && h('section', { className: 'ocv-card' }, h('form', { onSubmit: e => { e.preventDefault(); setPage(1); setRefreshKey(value => value + 1) }, className: 'ocv-row' }, field(t('search'), search, value => { setSearch(value); setPage(1) }, { placeholder: t('originalName') }), select(t('status'), filter, value => { setFilter(value); setPage(1) }, ['running', 'stopped', 'creating', 'error'].map(name => [name, name])), h('button', { type: 'submit', disabled: busy }, t('refresh'))),
          table([t('name'), t('status'), t('resources'), t('actions')], instances.list.map(row => h('tr', { key: row.id }, h('td', null, button(display(row), () => inspect(row)), h('small', null, original(row) + ' · #' + row.id), h('small', null, row.providerName || row.providerType || '')), h('td', null, h('span', { className: 'ocv-badge' }, row.status)), h('td', null, (row.cpu ?? '—') + ' CPU / ' + (row.memory ?? '—') + ' MB', h('small', null, row.ipAddress || row.publicIP || '')), h('td', null, h('div', { className: 'ocv-row' }, ...['start', 'stop', 'restart', 'alias', 'delete'].map(action => button(t(action), () => { setConfirmation(''); setAlias(row.alias || ''); setModal({ action: action === 'alias' ? 'instance_alias' : action + '_instance', row }) }, { key: action, className: action === 'delete' ? 'ocv-danger' : '', disabled: busy || row[({ start: 'canStart', stop: 'canStop', restart: 'canRestart', delete: 'canDelete' })[action]] === false }))))))), pager(page, setPage, instances.total)),
        connected && tab === 'create' && h('section', { className: 'ocv-card' }, h('h2', null, t('create')), h('p', { className: 'ocv-help' }, t('createHint')), h('form', { onSubmit: create, className: 'ocv-stack' }, h('div', { className: 'ocv-grid' }, select(t('provider'), draft.provider_id, value => updateDraft('provider_id', value), providers.map(row => [row.id, (row.name || row.id) + ' · ' + (row.type || '')])), select(t('instanceType'), draft.instance_type, value => { if (value) updateDraft('instance_type', value) }, [['container', t('container')], ['vm', t('vm')]])),
          admin && providerTotal > 20 && pager(providerPage, setProviderPage, providerTotal),
          catalogBusy && h('p', null, t('loading')), select(t('image'), draft.image_id, value => updateDraft('image_id', value), rows(catalog?.images).map(row => [row.id, (row.displayName || row.name) + ' · ' + row.architecture]), catalogBusy || !catalog),
          creationFields, select(t('network'), draft.network_type, value => updateDraft('network_type', value), networks.map(value => [value, value])), h('p', { className: 'ocv-help' }, t('defaultNetwork')), h('button', { type: 'submit', className: 'ocv-primary', disabled: busy || catalogBusy || !catalog || !draft.image_id || !draft.provider_id }, t(busy ? 'working' : 'create')))),
        connected && tab === 'providers' && h('section', { className: 'ocv-card' }, h('h2', null, t('providers')), table([t('provider'), t('status'), t('actions')], providers.map(row => h('tr', { key: row.id }, h('td', null, row.name, h('small', null, '#' + row.id + ' · ' + (row.type || ''))), h('td', null, row.status), h('td', null, h('div', { className: 'ocv-row' }, button(t('details'), () => setDetail({ data: row })), admin && button(t('health'), () => execute(async () => { const revision = epoch.current, data = await call('health_check', { provider_id: row.id }); if (revision === epoch.current) setDetail({ data }) })), admin && button(t('metrics'), () => execute(async () => { const revision = epoch.current, data = await call('get_metrics', { provider_id: row.id }); if (revision === epoch.current) setDetail({ data }) }))))))), admin && pager(providerPage, setProviderPage, providerTotal)),
        connected && tab === 'tasks' && h('section', { className: 'ocv-card' }, h('h2', null, t('tasks')), h('p', { className: 'ocv-help' }, t('taskHint')), table([t('task'), t('status'), t('progress'), t('details')], tasks.list.map(row => h('tr', { key: row.id }, h('td', null, row.taskType || row.type, h('small', null, '#' + row.id + ' · ' + (row.instanceName || row.instanceId || ''))), h('td', null, row.status), h('td', null, String(row.progress ?? 0) + '%'), h('td', null, row.errorMessage || row.message || row.status, h('details', null, h('summary', null, t('details')), json(row)))))), pager(taskPage, setTaskPage, tasks.total)),
        tab === 'settings' && h('section', { className: 'ocv-card ocv-stack' }, h('h2', null, t('settings')), check(t('sidebar'), sidebar, value => { setSidebar(value); saved.sidebar = value; persist(); updateSidebar(value) }), h('p', { className: 'ocv-help' }, t('sidebarHint')), connected && h('p', null, t(status.persistent ? 'persistent' : 'temporary') + ' · ' + status.credentialStore), h('p', { className: 'ocv-help' }, t('hostHint')), h('p', { className: 'ocv-help' }, t('headless')), h('code', null, 'ONE_CLICK_VIRT_API_URL / ONE_CLICK_VIRT_API_TOKEN'), h('p', { className: 'ocv-help' }, t('version') + ' ' + (status?.version || releaseVersion))),
        detail && h('section', { className: 'ocv-card', 'aria-label': t('details') }, h('div', { className: 'ocv-row ocv-between' }, h('h2', null, t('details') + (detail.id ? ' · #' + detail.id : '')), button(t('close'), () => setDetail(null))), detail.id && h('div', { className: 'ocv-row' }, button(t('alias'), () => { setAlias(detail.data.alias || ''); setModal({ action: 'instance_alias', row: { ...detail.data, id: detail.id } }) }), button(t('metrics'), () => execute(async () => { const revision = epoch.current, data = await call('get_metrics', { instance_id: detail.id }); if (revision === epoch.current) setDetail(old => ({ ...old, metrics: data })) })), button(t('diagnostics'), () => execute(async () => { const revision = epoch.current, data = await call('get_instance_logs', { instance_id: detail.id }); if (revision === epoch.current) setDetail(old => ({ ...old, diagnostics: data })) }))), json(detail.data), detail.metrics && json(detail.metrics), detail.diagnostics && h(React.Fragment, null, h('p', { className: 'ocv-help' }, t('logsHint')), json(detail.diagnostics))),
        modal && h('div', { className: 'ocv-modal-backdrop' }, h('section', { className: 'ocv-modal', role: 'dialog', 'aria-modal': true, 'aria-label': t('confirm') }, h('h2', null, t('confirm')), h('p', null, display(modal.row) + ' · ' + original(modal.row) + ' (#' + modal.row.id + ')'), modal.action === 'instance_alias' ? h(React.Fragment, null, field(t('alias'), alias, setAlias, { maxLength: 128, autoFocus: true }), h('p', { className: 'ocv-help' }, t('aliasHint'))) : modal.action === 'delete_instance' ? h(React.Fragment, null, h('p', { className: 'ocv-danger' }, t('deleteHint')), field(t('originalName'), confirmation, setConfirmation, { autoFocus: true, autoComplete: 'off' })) : h('p', null, t(modal.action.split('_')[0])), h('div', { className: 'ocv-actions' }, button(t('confirm'), doMutation, { className: modal.action === 'delete_instance' ? 'ocv-danger' : 'ocv-primary', disabled: busy || (modal.action === 'delete_instance' && confirmation !== original(modal.row)) }), button(t('cancel'), () => setModal(null)))))))
    }
    return { inject: ['slots', 'locale', 'layout'], apply(ctx) {
      disposed = false
      ctx.effect(() => ctx.locale.register('oneclickvirt', Object.fromEntries(Object.entries(dictionaries).map(([lang, values]) => [lang, Object.fromEntries(Object.entries(values).flatMap(([key, value]) => typeof value === 'string' ? [[key, value]] : Object.entries(value).map(([name, text]) => [key + '.' + name, text])))]))))
      const t = ctx.locale.bind('oneclickvirt')
      let sidebarDispose = null, showing = null
      updateSidebar = show => {
        if (disposed || show === showing) return
        showing = show
        if (typeof sidebarDispose === 'function') sidebarDispose()
        sidebarDispose = show ? ctx.slots.inject('sidebar.panellist', () => ctx.slots.register({ name: 'sidebar.panellist', id: 'oneclickvirt', order: 26, label: () => t('panel'), locale: 'oneclickvirt' }, Icon)) : null
      }
      ctx.slots.inject('main', () => ctx.slots.register({ name: 'main', key: 'oneclickvirt', locale: 'oneclickvirt' }, Seat))
      ctx.slots.inject('shell.overlay', () => ctx.slots.register({ name: 'shell.overlay', id: 'oneclickvirt-keeper', locale: 'oneclickvirt' }, Keeper))
      ctx.slots.inject('sidebar.panellist.actions', () => ctx.slots.register({ name: 'sidebar.panellist.actions', id: 'oneclickvirt-open', order: 81 }, ({ onDismiss }) => h('button', { type: 'button', className: 'ocv-menu', onClick: () => { ctx.layout.selectPanel('oneclickvirt'); onDismiss?.() } }, t('restore'))))
      updateSidebar(saved.sidebar)
      ctx.effect(() => () => { disposed = true; updateSidebar = () => {}; if (typeof sidebarDispose === 'function') sidebarDispose() })
    } }
  },
})
