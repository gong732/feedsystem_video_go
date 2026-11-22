
// ============ 全局状态 ============
const TOKEN_KEY = 'vf_token';
const USER_KEY  = 'vf_user';
let token = localStorage.getItem(TOKEN_KEY) || '';
let currentUser = JSON.parse(localStorage.getItem(USER_KEY) || 'null');
let feedCursor = 0;   // 关注流游标（兼容旧引用）
// B站式关注流状态：内存保留列表 + 双锚（顶部锚拉新 / 底部锚翻旧）
let feedCache = { items: [], topTs: 0, cursor: 0, ended: false };
let discoverCursor = 0; // 发现页游标（Redis 时间线位置，0=从头）
let discoverEnded = false; // 发现页是否到底（无限流停止条件）

// ============ 无限流：滚动到底自动加载 ============
let scrollTicking = false; // 节流：滚动事件 200ms 内只检查一次
function maybeLoadMore() {
  const feedList = $('feed-list');
  const discList = $('discover-list');
  if (!feedList && !discList) return; // 不在列表页（热榜/发布页不触发）
  // 接近底部（剩 300px），或内容不足一屏（无滚动条）时触发
  const nearBottom =
    (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 300) ||
    (document.documentElement.scrollHeight <= window.innerHeight + 60);
  if (!nearBottom) return;
  if (feedList) { if (feedCache.ended) return; loadFeed(); }
  else if (discList) { if (discoverEnded) return; loadDiscover(); }
}
window.addEventListener('scroll', function () {
  if (scrollTicking) return;
  scrollTicking = true;
  setTimeout(function () { scrollTicking = false; maybeLoadMore(); }, 200);
});

// ============ 工具 ============
function $(id) { return document.getElementById(id); }
function esc(s) { return String(s||'').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function toast(msg) {
  const t = $('toast'); t.textContent = msg; t.classList.add('show');
  clearTimeout(t._t); t._t = setTimeout(() => t.classList.remove('show'), 2000);
}
function fmtTime(iso) {
  const d = new Date(iso); const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return '刚刚';
  if (diff < 3600) return Math.floor(diff/60) + ' 分钟前';
  if (diff < 86400) return Math.floor(diff/3600) + ' 小时前';
  if (diff < 86400*30) return Math.floor(diff/86400) + ' 天前';
  return d.toLocaleDateString('zh-CN');
}
function fmtViews(n) {
  if (n >= 10000) return (n/10000).toFixed(1).replace(/\.0$/,'') + ' 万';
  if (n >= 1000) return (n/1000).toFixed(1).replace(/\.0$/,'') + ' k';
  return n;
}
function fmtDur(s) {
  if (!s) return '';
  const m = Math.floor(s/60), sec = s%60;
  return m + ':' + String(sec).padStart(2,'0');
}

// ============ API 封装 ============
let authExpiredNotified = false;

// 清掉本地登录态，并同步顶栏（不弹窗、不提示）
function clearAuthState() {
  token = '';
  currentUser = null;
  localStorage.removeItem(TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
  renderUserBox();
}

// 服务端判定 token 无效：清登录态；写操作直接弹登录框，读操作只提示一次
function handleAuthExpired(interactive) {
  clearAuthState();
  if (!authExpiredNotified) {
    authExpiredNotified = true;
    toast('登录已过期，请重新登录');
  }
  if (interactive) openAuth('login');
}

async function api(path, method = 'GET', body) {
  return apiRequest(path, method, body, true);
}

// allowRetry：公开读接口遇到过期 token 时，清掉 token 后匿名重试一次，
// 避免用户因为一个过期 token 连发现页都看不了。
async function apiRequest(path, method, body, allowRetry) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers['Authorization'] = 'Bearer ' + token;
  const res = await fetch('/api' + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined
  });
  const json = await res.json().catch(() => ({ code: res.status, msg: '响应解析失败' }));
  // 只有"确实带了 token"才按登录失效处理，避免把匿名请求的 401 当成过期
  if (res.status === 401 && token && !path.startsWith('/users/login') && !path.startsWith('/users/register')) {
    handleAuthExpired(method !== 'GET');
    if (method === 'GET' && allowRetry) return apiRequest(path, method, body, false);
    throw new Error(json.msg || '登录已过期，请重新登录');
  }
  if (json.code !== 0) throw new Error(json.msg || '请求失败');
  return json.data;
}

// ============ 用户 / 登录 ============
function renderUserBox() {
  const box = $('userbox');
  if (currentUser) {
    box.innerHTML =
      '<div class="avatar">' + esc((currentUser.nickname || currentUser.username).slice(0,1)) + '</div>' +
      '<span>' + esc(currentUser.nickname || currentUser.username) + '</span>' +
      '<button class="btn btn-ghost" onclick="logout()">退出</button>';
  } else {
    box.innerHTML = '<button class="btn btn-primary" onclick="openAuth(\'login\')">登录 / 注册</button>';
  }
}

function logout() {
  token = ''; currentUser = null;
  authExpiredNotified = false;
  localStorage.removeItem(TOKEN_KEY); localStorage.removeItem(USER_KEY);
  renderUserBox(); showView('discover'); toast('已退出登录');
}

let authMode = 'login';
function openAuth(mode) {
  authMode = mode;
  $('auth-title').textContent = mode === 'login' ? '登录' : '注册';
  $('auth-submit').textContent = mode === 'login' ? '登录' : '注册';
  $('nickname-field').style.display = mode === 'login' ? 'none' : 'block';
  $('auth-msg').textContent = '';
  $('auth-switch').innerHTML = mode === 'login' ? '没有账号？<a id="auth-switch-link">去注册</a>' : '已有账号？<a id="auth-switch-link">去登录</a>';
  $('auth-switch-link').onclick = () => openAuth(mode === 'login' ? 'register' : 'login');
  $('auth-modal').style.display = 'flex';
}
$('auth-switch-link').onclick = () => openAuth('register');

$('auth-submit').onclick = async () => {
  const username = $('auth-username').value.trim();
  const password = $('auth-password').value;
  const nickname = $('auth-nickname').value.trim();
  const msg = $('auth-msg');
  if (!username || !password) { msg.className = 'form-msg err'; msg.textContent = '用户名和密码不能为空'; return; }
  try {
    if (authMode === 'login') {
      const data = await api('/users/login', 'POST', { username, password });
      token = data.token; currentUser = data.user;
      authExpiredNotified = false;
      localStorage.setItem(TOKEN_KEY, token); localStorage.setItem(USER_KEY, JSON.stringify(data.user));
      $('auth-modal').style.display = 'none';
      renderUserBox(); showView('discover'); toast('欢迎回来，' + (currentUser.nickname || currentUser.username));
    } else {
      await api('/users/register', 'POST', { username, password, nickname });
      msg.className = 'form-msg ok'; msg.textContent = '注册成功，请登录';
      openAuth('login');
      $('auth-username').value = username;
    }
  } catch (e) {
    msg.className = 'form-msg err'; msg.textContent = e.message;
  }
};

// ============ 视图切换 ============
function showView(name) {
  document.querySelectorAll('.nav button').forEach(b => b.classList.remove('active'));
  const btn = document.querySelector('.nav button[data-view="' + name + '"]');
  if (btn) btn.classList.add('active');
  if ((name === 'feed' || name === 'publish') && !currentUser) {
    toast('请先登录'); openAuth('login');
    return;
  }
  renderView(name);
}

document.querySelectorAll('.nav button').forEach(b => {
  b.onclick = () => showView(b.dataset.view);
});

// ============ 视图渲染 ============
function renderView(name) {
  const main = $('main');
  if (name === 'discover') renderDiscover(main);
  else if (name === 'hot') renderHot(main);
  else if (name === 'feed') renderFeed(main);
  else if (name === 'publish') renderPublish(main);
}

// ---- 发现（全站时间线，Redis 游标分页） ----
function renderDiscover(main) {
  discoverCursor = 0;
  discoverEnded = false;
  main.innerHTML =
    '<div class="view-title">发现<span>全站最新视频</span></div>' +
    '<div class="grid" id="discover-list">' + skeletonHTML(9) + '</div>' +
    '<div class="load-more" id="discover-more"><span class="more-hint">加载中...</span></div>';
  loadDiscover();
}
// 首屏骨架屏：先占住版式，数据回来再替换，避免"加载中"三个字闪一下
function skeletonHTML(n) {
  let out = '';
  for (let i = 0; i < n; i++) {
    out += '<div class="sk-card">' +
      '<div class="sk-thumb"></div>' +
      '<div class="sk-line" style="width:86%"></div>' +
      '<div class="sk-line" style="width:52%"></div>' +
      '</div>';
  }
  return out;
}
async function loadDiscover() {
  const list = $('discover-list');
  if (!list || list.dataset.loading) return;
  list.dataset.loading = '1';
  const status = $('discover-more');
  if (status) status.innerHTML = '<span class="more-hint">加载中...</span>';
  try {
    const data = await api('/videos?cursor=' + discoverCursor + '&limit=6');
    if (discoverCursor === 0 && data.list.length === 0) {
      list.innerHTML = '<div class="empty"><div class="big">📭</div>还没有视频，去发布第一个吧</div>';
      if (status) status.style.display = 'none';
    } else {
      if (discoverCursor === 0) list.innerHTML = ''; // 清掉骨架屏，换成真实卡片
      list.insertAdjacentHTML('beforeend', data.list.map(v => cardHTML(v, true)).join(''));
      initFollowStates();
      if (data.next_cursor && data.next_cursor > 0) {
        discoverCursor = data.next_cursor; // 还有更多，记住位置
        if (status) status.innerHTML = '<span class="more-hint">上滑加载更多</span>';
      } else {
        discoverEnded = true; // next_cursor=0 = 到底
        if (status) status.innerHTML = '<span class="more-end">— 没有更多了 —</span>';
      }
    }
  } catch (e) {
    if (discoverCursor === 0) list.innerHTML = '';
    list.insertAdjacentHTML('beforeend', '<div class="empty">加载失败：' + esc(e.message) + '</div>');
    if (status) status.innerHTML = '<span class="more-hint">加载失败，上滑重试</span>';
  }
  delete list.dataset.loading;
  // 内容不足一屏时没有滚动事件，主动补一次检查
  setTimeout(maybeLoadMore, 60);
}

// ---- 热榜 ----
async function renderHot(main) {
  main.innerHTML = '<div class="view-title">热榜<span>播放量驱动的实时榜单</span></div><div id="hot-list"><div class="loading">加载中...</div></div>';
  try {
    const list = await api('/hot?limit=10');
    const box = $('hot-list');
    if (!list || list.length === 0) {
      box.innerHTML = '<div class="empty"><div class="big">🌡️</div>榜单还是空的，多看几个视频就有了</div>';
      return;
    }
    box.innerHTML = list.map((v, i) =>
      '<div class="hot-item" onclick="openDetail(' + v.id + ')">' +
        '<div class="hot-rank">' + (i+1) + '</div>' +
        '<div class="hot-info"><div class="hot-title">' + esc(v.title) + '</div>' +
        '<div class="hot-meta">' + esc((v.author && (v.author.nickname || v.author.username)) || '') + ' · ' + fmtTime(v.created_at) + '</div></div>' +
        '<div class="hot-views">▶ ' + fmtViews(v.views) + '</div>' +
      '</div>'
    ).join('');
  } catch (e) {
    $('hot-list').innerHTML = '<div class="empty">加载失败：' + esc(e.message) + '</div>';
  }
}

// ---- 关注流（B站式：增量拉新插顶 + 游标翻旧追加） ----
function renderFeed(main) {
  main.innerHTML =
    '<div class="view-title">关注流<span>你关注的人的最新视频</span></div>' +
    '<div class="feed-tools">' +
      '<button class="btn btn-ghost" onclick="refreshFeed()">↻ 刷新新视频</button>' +
      '<span id="feed-hint" class="feed-hint"></span>' +
    '</div>' +
    '<div class="list" id="feed-list"></div>' +
    '<div class="load-more" id="feed-more"><span class="more-hint">加载中...</span></div>';
  if (feedCache.items.length > 0) {
    // 有缓存 → 先渲染已有列表（不清空），再拉新插顶
    $('feed-list').insertAdjacentHTML('beforeend', feedCache.items.map(v => cardHTML(v)).join(''));
    initFollowStates();
    refreshFeed();
    const status = $('feed-more');
    if (feedCache.ended) { if (status) status.innerHTML = '<span class="more-end">— 没有更多了 —</span>'; }
    setTimeout(maybeLoadMore, 60);
  } else {
    loadFeed(); // 首次：从头拉
  }
}

// 拉新：刷新时只拿比顶部锚（topTs）更新的，插到列表头部（增量插顶，B站式）
async function refreshFeed() {
  const list = $('feed-list');
  if (!list || list.dataset.refreshing) return;
  list.dataset.refreshing = '1';
  try {
    if (feedCache.topTs <= 0) { delete list.dataset.refreshing; return; }
    const data = await api('/feed?since=' + feedCache.topTs);
    const fresh = data.list.filter(v => !feedCache.items.some(x => x.id === v.id));
    if (fresh.length > 0) {
      feedCache.items = fresh.concat(feedCache.items);   // 插到内存列表头部
      list.insertAdjacentHTML('afterbegin', fresh.map(v => cardHTML(v)).join(''));
      initFollowStates();
      feedCache.topTs = Date.parse(fresh[0].created_at); // 更新顶部锚（毫秒，与后端 created_ts 同基准）
      const h = $('feed-hint'); if (h) h.textContent = '拉到 ' + fresh.length + ' 条新视频 ↑';
      setTimeout(() => { const h2 = $('feed-hint'); if (h2) h2.textContent = ''; }, 2500);
    }
  } catch (e) { /* 拉新失败不打断浏览 */ }
  delete list.dataset.refreshing;
}

// 翻旧：带底部锚（cursor）加载更老的，追加到列表尾部
async function loadFeed() {
  const list = $('feed-list');
  if (!list || list.dataset.loading) return;
  list.dataset.loading = '1';
  const status = $('feed-more');
  if (status) status.innerHTML = '<span class="more-hint">加载中...</span>';
  try {
    const q = feedCache.cursor > 0 ? '?cursor=' + feedCache.cursor : '';
    const data = await api('/feed' + q);
    if (feedCache.items.length === 0 && data.list.length === 0) {
      list.innerHTML = '<div class="empty"><div class="big">👣</div>你还没有关注任何人<br><span style="font-size:12px;">去"发现"页关注几个作者，这里就会出现他们的视频</span></div>';
      if (status) status.style.display = 'none';
      delete list.dataset.loading; return;
    }
    const fresh = data.list.filter(v => !feedCache.items.some(x => x.id === v.id));
    if (fresh.length > 0) {
      feedCache.items = feedCache.items.concat(fresh);   // 追加到内存列表尾部
      list.insertAdjacentHTML('beforeend', fresh.map(v => cardHTML(v)).join(''));
      initFollowStates();
    }
    if (feedCache.topTs === 0 && fresh.length > 0) {
      feedCache.topTs = Date.parse(fresh[0].created_at); // 首次：设顶部锚（毫秒）
    }
    feedCache.cursor = data.next_cursor || 0;            // 更新底部锚
    if (data.list.length < 10) {
      feedCache.ended = true;                            // 到底：无限流停止
      if (status) status.innerHTML = '<span class="more-end">— 没有更多了 —</span>';
    } else if (status) {
      status.innerHTML = '<span class="more-hint">上滑加载更多</span>';
    }
  } catch (e) { list.insertAdjacentHTML('beforeend', '<div class="empty">加载失败：' + esc(e.message) + '</div>'); }
  delete list.dataset.loading;
  // 内容不足一屏时没有滚动事件，主动补一次检查
  setTimeout(maybeLoadMore, 60);
}

// ---- 发布 ----
function renderPublish(main) {
  main.innerHTML =
    '<div class="form-card">' +
      '<h3>发布新视频</h3>' +
      '<div class="field"><label>标题 *</label><input id="pub-title" placeholder="视频标题"></div>' +
      '<div class="field"><label>描述</label><textarea id="pub-desc" placeholder="简单介绍一下"></textarea></div>' +
      '<div class="field"><label>视频文件 *</label><input id="pub-file" type="file" accept="video/mp4">' +
      '<div class="hint">选本地 mp4（≤200MB），会先上传到本项目 static/uploads 再发布</div></div>' +
      '<div class="field"><label>或填视频地址（选了文件就不用填）</label><input id="pub-url" placeholder="/static/videos/xxx.mp4 或 https://..."></div>' +
      '<div class="field"><label>封面图</label><input id="pub-cover-file" type="file" accept="image/jpeg,image/png,image/webp">' +
      '<div class="hint">可选，jpg/png/webp，≤10MB</div></div>' +
      '<div class="field"><label>或填封面地址</label><input id="pub-cover" placeholder="/static/covers/xxx.jpg（可选）"></div>' +
      '<div class="field"><label>时长（秒）</label><input id="pub-dur" type="number" placeholder="如 15"></div>' +
      '<div class="form-msg" id="pub-msg"></div>' +
      '<button class="btn btn-primary" style="width:100%;padding:11px;" onclick="doPublish()">发布</button>' +
    '</div>';
}

// 上传单个文件，返回后端给的站内 URL（/static/uploads/...）
async function uploadFileTo(endpoint, file) {
  const fd = new FormData();
  fd.append('file', file);
  const headers = {};
  if (token) headers['Authorization'] = 'Bearer ' + token;
  const res = await fetch('/api' + endpoint, { method: 'POST', headers, body: fd });
  const json = await res.json().catch(() => ({ code: res.status, msg: '响应解析失败' }));
  if (res.status === 401 && token) {
    handleAuthExpired(true);
    throw new Error(json.msg || '登录已过期，请重新登录');
  }
  if (json.code !== 0) throw new Error(json.msg || '上传失败');
  return json.data.url;
}

async function doPublish() {
  const msg = $('pub-msg');
  const title = $('pub-title').value.trim();
  if (!title) { msg.className = 'form-msg err'; msg.textContent = '标题必填'; return; }

  const vFile = $('pub-file').files[0];
  const cFile = $('pub-cover-file').files[0];
  let url = $('pub-url').value.trim();
  let cover = $('pub-cover').value.trim();
  if (!vFile && !url) { msg.className = 'form-msg err'; msg.textContent = '请选择视频文件，或填写视频地址'; return; }

  try {
    // 先传文件拿 URL，再带着 URL 调发布接口（发布接口本身只认 URL 字符串）
    if (vFile) {
      msg.className = 'form-msg'; msg.textContent = '上传视频中（' + (vFile.size / 1048576).toFixed(1) + 'MB）...';
      url = await uploadFileTo('/upload/video', vFile);
    }
    if (cFile) {
      msg.textContent = '上传封面中...';
      cover = await uploadFileTo('/upload/cover', cFile);
    }
    msg.textContent = '发布中...';
    await api('/videos', 'POST', {
      title,
      description: $('pub-desc').value.trim(),
      video_url: url,
      cover_url: cover,
      duration: parseInt($('pub-dur').value) || 0
    });
    msg.className = 'form-msg ok'; msg.textContent = '发布成功！';
    toast('发布成功 🎉');
    setTimeout(() => showView('discover'), 800);
  } catch (e) {
    msg.className = 'form-msg err'; msg.textContent = e.message;
  }
}

// ============ 视频卡片 ============
function cardHTML(v, grid) {
  const authorName = (v.author && (v.author.nickname || v.author.username)) || '未知作者';
  const cover = v.cover_url
    ? '<img src="' + esc(v.cover_url) + '" alt="" loading="lazy" decoding="async" onerror="this.outerHTML=\'<div class=placeholder>▶</div>\'">'
    : '<div class="placeholder">▶</div>';
  const followBtn = (currentUser && v.author && v.author.id !== currentUser.id)
    ? '<button class="follow-btn" data-aid="' + v.author.id + '" title="关注该作者" onclick="event.stopPropagation();toggleFollow(this)">关注</button>'
    : '';
  return (
    '<div class="' + (grid ? 'card card--grid' : 'card') + '" onclick="openDetail(' + v.id + ')">' +
      '<div class="thumb">' + cover + (v.duration ? '<div class="dur">' + fmtDur(v.duration) + '</div>' : '') + '</div>' +
      '<div class="card-body">' +
        '<div class="card-title">' + esc(v.title) + '</div>' +
        (grid ? '' : (v.description ? '<div class="desc">' + esc(v.description) + '</div>' : '')) +
        '<div class="card-meta">' +
          '<span class="author"><span class="mini-avatar">' + esc(authorName.slice(0,1)) + '</span>' + esc(authorName) + followBtn + '</span>' +
          '<span class="views">▶ ' + fmtViews(v.views) + '</span>' +
          '<button class="like-btn' + (v.liked ? ' liked' : '') + '" data-vid="' + v.id + '"' +
            ' onclick="event.stopPropagation();toggleLike(' + v.id + ', this)">' +
            (v.liked ? '❤' : '♡') + ' ' + (v.likes_count || 0) + '</button>' +
          (grid ? '' : '<span>' + fmtTime(v.created_at) + '</span>') +
        '</div>' +
      '</div>' +
    '</div>'
  );
}

// 初始化卡片上所有关注按钮的已关注状态（刷新后不丢状态）
// 逐个调 is_following 查询——骨架阶段视频量小够用；
// 数据量大时应让后端在列表接口里批量带 is_followed 字段（避免 N 次请求）
async function initFollowStates() {
  if (!currentUser) return;
  const aids = [...new Set([...document.querySelectorAll('.follow-btn')].map(b => b.dataset.aid))];
  for (const aid of aids) {
    try {
      const ok = await api('/is_following/' + aid);
      document.querySelectorAll('.follow-btn[data-aid="' + aid + '"]').forEach(b => {
        if (ok) { b.classList.add('done'); b.textContent = '已关注'; }
      });
    } catch (e) { /* 未登录等场景静默跳过 */ }
  }
}

// 关注 / 取消关注（toggle，和 Go 后端一样靠唯一索引）
async function toggleFollow(btn) {
  if (!currentUser) { openAuth('login'); return; }
  const aid = btn.dataset.aid;
  const wasDone = btn.classList.contains('done');
  btn.textContent = '...';
  try {
    const msg = await api('/follow/' + aid, 'POST');
    const isCancel = msg && msg.indexOf('取消') >= 0;
    btn.classList.toggle('done');
    btn.textContent = btn.classList.contains('done') ? '已关注' : '关注';
    toast(msg);
  } catch (e) {
    btn.textContent = wasDone ? '已关注' : '关注';
    toast(e.message);
  }
}

// ============ 视频详情 ============
async function openDetail(id) {
  const modal = $('detail-modal');
  modal.style.display = 'flex';
  $('detail-player').innerHTML = '<div class="loading">加载中...</div>';
  $('detail-body').innerHTML = '';
  try {
    const v = await api('/videos/' + id);
    let playerHTML;
    if (v.video_url) {
      playerHTML =
        '<video controls autoplay playsinline>' +
          '<source src="' + esc(v.video_url) + '" type="video/mp4">' +
          '浏览器不支持视频播放' +
        '</video>';
    } else {
      playerHTML = '<div class="no-video"><div style="font-size:34px;">🎞️</div>该视频暂无播放地址</div>';
    }
    $('detail-player').innerHTML = playerHTML;

    const authorName = (v.author && (v.author.nickname || v.author.username)) || '未知作者';
    $('detail-body').innerHTML =
      '<div class="detail-title">' + esc(v.title) + '</div>' +
      '<div class="detail-meta">' +
        '<span class="author"><span class="mini-avatar">' + esc(authorName.slice(0,1)) + '</span>' + esc(authorName) + '</span>' +
        '<span>▶ ' + fmtViews(v.views) + '</span>' +
        '<span>' + fmtTime(v.created_at) + '</span>' +
        '<button class="like-btn' + (v.liked ? ' liked' : '') + '" data-vid="' + v.id + '" id="like-btn"' +
          ' onclick="toggleLike(' + v.id + ', this)">' +
          (v.liked ? '❤' : '♡') + ' <span id="like-count">' + (v.likes_count || 0) + '</span></button>' +
        (currentUser && v.author && v.author.id !== currentUser.id
          ? '<button class="btn btn-ghost" style="font-size:12px;padding:4px 12px;" onclick="event.stopPropagation();toggleFollowFromDetail(' + v.author.id + ', this)">关注</button>'
          : '') +
      '</div>' +
      (v.description ? '<div class="detail-desc">' + esc(v.description) + '</div>' : '') +
      '<div class="comments">' +
        '<div class="comments-head">评论 <span id="cm-total">' + (v.comments_count || 0) + '</span></div>' +
        '<div id="cm-list"><div class="loading">加载中...</div></div>' +
        '<div class="cm-more" id="cm-more"></div>' +
        '<div class="cm-form">' +
          '<input id="cm-input" maxlength="500" placeholder="' + (currentUser ? '说点什么…（最多 500 字）' : '登录后才能评论') + '"' + (currentUser ? '' : ' disabled') + '>' +
          '<button class="btn btn-primary" onclick="submitComment(' + v.id + ')"' + (currentUser ? '' : ' disabled') + '>发送</button>' +
        '</div>' +
      '</div>';

    // 初始化详情页关注按钮的已关注状态
    const dFollowBtn = $('detail-body').querySelector('.btn-ghost');
    if (dFollowBtn) {
      try {
        const ok = await api('/is_following/' + v.author.id);
        if (ok) { dFollowBtn.textContent = '已关注'; }
      } catch (e) {}
    }

    // 评论列表：首屏取最新 10 条
    await loadComments(v.id, 0, true);

    const vid = $('detail-player').querySelector('video');
    if (vid) {
      vid.onerror = () => {
        $('detail-player').innerHTML = '<div class="no-video"><div style="font-size:34px;">🚫</div>视频文件无法播放（URL 可能无效）<div style="font-size:12px;">' + esc(v.video_url || '') + '</div></div>';
      };
    }
  } catch (e) {
    $('detail-player').innerHTML = '<div class="no-video"><div style="font-size:34px;">😵</div>' + esc(e.message) + '</div>';
  }
}
async function toggleFollowFromDetail(aid, btn) {
  btn.textContent = '...';
  try {
    const msg = await api('/follow/' + aid, 'POST');
    btn.textContent = (msg && msg.indexOf('取消') >= 0) ? '已关注' : '关注';
    toast(msg);
  } catch (e) { toast(e.message); }
}
// ============ 点赞 ============
// 点赞是 toggle：同一个按钮点一次赞、再点一次取消。
// 后端返回最新的 liked / likes_count，前端直接以服务端结果为准（不在本地猜）。
async function toggleLike(videoId, btn) {
  if (!currentUser) { openAuth('login'); return; }
  btn.disabled = true;
  try {
    const data = await api('/videos/' + videoId + '/like', 'POST');
    // 同步所有这个视频的点赞按钮（卡片上的 + 详情里的）
    document.querySelectorAll('.like-btn[data-vid="' + videoId + '"]').forEach(b => {
      b.classList.toggle('liked', data.liked);
      if (b.id === 'like-btn') {
        b.innerHTML = (data.liked ? '❤' : '♡') + ' <span id="like-count">' + data.likes_count + '</span>';
      } else {
        b.innerHTML = (data.liked ? '❤' : '♡') + ' ' + data.likes_count;
      }
    });
  } catch (e) {
    toast(e.message);
  } finally {
    btn.disabled = false;
  }
}

// ============ 评论 ============
let commentCursor = 0;

function commentHTML(videoId, cm) {
  const name = (cm.author && (cm.author.nickname || cm.author.username)) || '用户';
  const canDel = currentUser && cm.user_id === currentUser.id;
  return '<div class="cm-item">' +
    '<span class="mini-avatar">' + esc(name.slice(0, 1)) + '</span>' +
    '<div class="cm-main">' +
      '<div class="cm-top">' +
        '<span class="cm-name">' + esc(name) + '</span>' +
        '<span>' + fmtTime(cm.created_at) + '</span>' +
        (canDel ? '<button class="cm-del" onclick="deleteComment(' + videoId + ',' + cm.id + ')">删除</button>' : '') +
      '</div>' +
      '<div class="cm-text">' + esc(cm.content) + '</div>' +
    '</div>' +
  '</div>';
}

// reset=true 表示首屏/重新拉取；否则是"加载更多"续接
async function loadComments(videoId, cursor, reset) {
  const box = $('cm-list');
  if (!box) return;
  if (reset) {
    commentCursor = 0;
    box.innerHTML = '<div class="loading">加载中...</div>';
    const more = $('cm-more'); if (more) more.innerHTML = '';
  }
  try {
    const data = await api('/videos/' + videoId + '/comments?cursor=' + cursor + '&limit=10');
    const items = data.list || [];
    if (reset) box.innerHTML = '';
    if (!items.length && reset) {
      box.innerHTML = '<div class="cm-empty">还没有评论，来抢沙发</div>';
    }
    box.insertAdjacentHTML('beforeend', items.map(cm => commentHTML(videoId, cm)).join(''));

    commentCursor = data.next_cursor || 0;
    const more = $('cm-more');
    if (more) {
      more.innerHTML = commentCursor
        ? '<button onclick="loadComments(' + videoId + ', ' + commentCursor + ', false)">加载更多评论</button>'
        : '';
    }
  } catch (e) {
    if (reset) box.innerHTML = '<div class="cm-empty">评论加载失败：' + esc(e.message) + '</div>';
  }
}

async function submitComment(videoId) {
  if (!currentUser) { openAuth('login'); return; }
  const input = $('cm-input');
  const content = (input.value || '').trim();
  if (!content) { toast('评论不能为空'); return; }
  try {
    await api('/videos/' + videoId + '/comments', 'POST', { content });
    input.value = '';
    const total = $('cm-total');
    if (total) total.textContent = (parseInt(total.textContent) || 0) + 1;
    toast('评论成功');
    await loadComments(videoId, 0, true); // 重新拉第一页，保证顺序正确
  } catch (e) {
    toast(e.message);
  }
}

async function deleteComment(videoId, commentId) {
  if (!confirm('确定删除这条评论吗？')) return;
  try {
    await api('/comments/' + commentId, 'DELETE');
    const total = $('cm-total');
    if (total) total.textContent = Math.max(0, (parseInt(total.textContent) || 0) - 1);
    toast('已删除');
    await loadComments(videoId, 0, true);
  } catch (e) {
    toast(e.message);
  }
}


// 点遮罩和关闭按钮都要暂停视频（否则弹层关了声音还在放）
$('detail-close').onclick = () => {
  const v = $('detail-player').querySelector('video');
  if (v) v.pause();
  $('detail-modal').style.display = 'none';
};
$('detail-modal').onclick = (e) => {
  if (e.target === $('detail-modal')) {
    const v = $('detail-player').querySelector('video');
    if (v) v.pause();
    $('detail-modal').style.display = 'none';
  }
};

// ============ 启动 ============
renderUserBox();
showView('discover');
