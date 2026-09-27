// 寰星云科 API 文档 · 多页站布局脚本
// 每个 HTML 页面声明 window.__PAGE__ = { slug, section, title }，由本文件统一渲染 header/sidebar/breadcrumb/prev-next。

const SITE = {
  brand: '寰星云科',
  brandSub: 'Huanxing API Docs',
  brandHref: '/',
  logo: '/docs/logo.png',
  footer: [
    '寰星云科科技有限公司',
    'Huanxing Yunke Technology Co., Ltd',
  ],
};

let API_BASE_URL = location.origin;
let SEEDANCE_API_BASE_URL = '';

const ASSET_ACTION_TEMPLATES = {
  CreateAssetGroup: {
    Name: '角色素材库',
    Description: '用于存放角色参考图和视频',
  },
  CreateVisualValidateSession: {
  },
  CreateRealValidateH5: {
  },
  GetVisualValidateResult: {
    BytedToken: 'example-token',
  },
  CreateAsset: {
    GroupId: '',
    URL: 'https://example.com/person.jpg',
    AssetType: 'Image',
    Name: '人物正面照',
  },
  GetAsset: { Id: '' },
  GetAssetGroup: { Id: '' },
  DeleteAsset: { Id: '' },
  DeleteAssetGroup: { Id: '' },
};

const NAV_TOP = [
  { href: '/docs/register/3-quota.html', label: '快速开始', matchPrefix: '/docs/register/' },
  { href: '/docs/token/1-intro.html', label: '模型分组', matchPrefix: '/docs/token/' },
  { href: '/docs/ccswitch/1-common.html', label: 'CC-Switch', matchPrefix: '/docs/ccswitch/' },
  { href: '/docs/cli/2-claude.html', label: 'CLI 配置', matchPrefix: '/docs/cli/' },
  { href: '/docs/tools/workbuddy.html', label: '工具接入', matchPrefix: '/docs/tools/' },
  { href: '/docs/paint/GPTImage.html', label: '绘图模型', matchPrefix: '/docs/paint/' },
  { href: '/docs/seedance/generation.html', label: '视频模型', matchPrefix: '/docs/seedance/' },
  { href: '/docs/advanced/DeepSeekClaudeCode.html', label: '进阶玩法', matchPrefix: '/docs/advanced/' },
  { href: '/docs/faq/CC.html', label: '常见问题', matchPrefix: '/docs/faq/' },
];

const NAV_SIDEBAR = [
  {
    title: '快速开始',
    items: [
      { href: '/docs/register/3-quota.html', label: '（1）购买额度' },
      { href: '/docs/register/4-token.html', label: '（2）创建 API 令牌' },
      { href: '/docs/register/5-env.html', label: '（3）环境检查（必要）' },
      { href: '/docs/register/6-cli.html', label: '（4）安装 CLI 工具' },
    ],
  },
  {
    title: '模型分组介绍',
    items: [
      { href: '/docs/token/1-intro.html', label: '模型广场' },
      { href: '/docs/token/2-group.html', label: '令牌分组介绍' },
    ],
  },
  {
    title: 'CC-Switch 使用',
    items: [
      { href: '/docs/ccswitch/1-common.html', label: '通用步骤' },
      { href: '/docs/ccswitch/2-claude.html', label: 'Claude Code 配置' },
      { href: '/docs/ccswitch/3-codex.html', label: 'Codex 配置' },
      { href: '/docs/ccswitch/4-gemini.html', label: 'Gemini 配置' },
    ],
  },
  {
    title: 'CLI 配置教程',
    items: [
      { href: '/docs/cli/2-claude.html', label: 'Claude Code 配置' },
      { href: '/docs/cli/3-codex.html', label: 'Codex 配置' },
      { href: '/docs/cli/4-gemini.html', label: 'Gemini 配置' },
    ],
  },
  {
    title: '工具接入教程',
    items: [
      { href: '/docs/tools/workbuddy.html', label: 'WorkBuddy 接入' },
      { href: '/docs/tools/cursor.html', label: 'Cursor 接入' },
      { href: '/docs/tools/TRAE.html', label: 'TRAE 接入' },
    ],
  },
  {
    title: '绘图模型教程',
    items: [
      { href: '/docs/paint/GPTImage.html', label: 'GPT-Image-2' },
    ],
  },
  {
    title: '视频模型教程',
    items: [
      {
        href: '/docs/seedance/generation.html',
        label: 'doubao-seedance-2.0 视频生成(Seedance 原生格式)',
      },
      {
        href: '/docs/seedance/generation-openai.html',
        label: 'doubao-seedance-2.0 视频生成(openai格式)',
      },
      {
        href: '/docs/seedance/assets.html',
        label: '素材库 API',
      },
    ],
  },
  {
    title: '进阶玩法',
    items: [
      { href: '/docs/advanced/DeepSeekClaudeCode.html', label: 'DS 接入 CC' },
    ],
  },
  {
    title: '常见问题',
    items: [
      { href: '/docs/faq/CC.html', label: 'Claude Code' },
      { href: '/docs/faq/Codex.html', label: 'Codex' },
      { href: '/docs/faq/Gemini.html', label: 'Gemini' },
    ],
  },
];

function currentPath() {
  let p = location.pathname || '/';
  // server fallback maps unknown to index.html, normalize trailing
  if (p.endsWith('/index.html')) p = p.replace(/index\.html$/, '');
  return p;
}

function pathsMatch(a, b) {
  const norm = (s) => s.replace(/\/index\.html$/, '/').replace(/\/+$/, '/');
  return norm(a) === norm(b);
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

function renderTopbar() {
  const path = currentPath();
  const items = NAV_TOP.map((n) => {
    const isActive = path.startsWith(n.matchPrefix);
    return `<a href="${n.href}" class="${isActive ? 'active' : ''}">${escapeHTML(n.label)}</a>`;
  }).join('');
  return `
    <header class="topbar">
      <a class="brand" href="${SITE.brandHref}" aria-label="返回文档首页">
        <img src="${SITE.logo}" alt="${escapeHTML(SITE.brand)}" />
        <span>
          <strong>${escapeHTML(SITE.brand)}</strong>
          <small>${escapeHTML(SITE.brandSub)}</small>
        </span>
      </a>
      <nav class="topnav" aria-label="顶部导航">${items}</nav>
      <button class="menu-button" type="button" aria-label="打开目录" aria-expanded="false">
        <span></span><span></span><span></span>
      </button>
    </header>
  `;
}

async function loadSiteConfig() {
  try {
    const response = await fetch('/api/status', {
      cache: 'no-store',
      credentials: 'same-origin',
    });
    if (!response.ok) return;

    const responseBody = await response.json();
    const status = responseBody.data || responseBody;
    const serverAddress = String(status.server_address || '').trim().replace(/\/+$/, '');
    if (serverAddress) {
      SITE.brandHref = serverAddress;
      API_BASE_URL = serverAddress;
    }
    SEEDANCE_API_BASE_URL = String(status.seedance_api_url || '').trim().replace(/\/+$/, '');
  } catch {
    // Keep the local root fallback when the API is unavailable.
  }
}

function renderSidebar() {
  const path = currentPath();
  const home = `<a class="sidebar-home" href="/docs/">寰星云科 API 使用文档</a>`;
  const groups = NAV_SIDEBAR.map((g) => {
    const items = g.items.map((it) => {
      const active = pathsMatch(it.href, path) ? ' active' : '';
      let html = `<a class="sidebar-child${active}" href="${it.href}">${escapeHTML(it.label)}</a>`;
      if (it.children && it.children.length) {
        html += it.children.map((child) => {
          const childActive = pathsMatch(child.href, path) ? ' active' : '';
          return `<a class="sidebar-child nested${childActive}" href="${child.href}">${escapeHTML(child.label)}</a>`;
        }).join('');
      }
      return html;
    }).join('');
    return `
      <div class="sidebar-group">
        <span class="sidebar-section">${escapeHTML(g.title)}</span>
        ${items}
      </div>
    `;
  }).join('');
  return home + groups;
}

function flatPages() {
  return NAV_SIDEBAR.flatMap((g) => g.items.flatMap((it) => {
    const entries = [{ href: it.href, label: it.label, section: g.title }];
    if (it.children && it.children.length) {
      it.children.forEach((child) => {
        entries.push({ href: child.href, label: child.label, section: g.title, parent: it.label });
      });
    }
    return entries;
  }));
}

function renderBreadcrumb() {
  const path = currentPath();
  const all = flatPages();
  const current = all.find((p) => pathsMatch(p.href, path));
  const parts = [
    `<a href="/docs/">寰星云科 API 文档</a>`,
  ];
  if (current) {
    parts.push(`<span class="sep">›</span>`);
    parts.push(`<span>${escapeHTML(current.section)}</span>`);
    if (current.parent) {
      parts.push(`<span class="sep">›</span>`);
      parts.push(`<span>${escapeHTML(current.parent)}</span>`);
    }
    parts.push(`<span class="sep">›</span>`);
    parts.push(`<span class="current">${escapeHTML(current.label)}</span>`);
  }
  return parts.join(' ');
}

function renderPrevNext() {
  const path = currentPath();
  const all = flatPages();
  const idx = all.findIndex((p) => pathsMatch(p.href, path));
  if (idx === -1) return '';
  const prev = idx > 0 ? all[idx - 1] : null;
  const next = idx < all.length - 1 ? all[idx + 1] : null;
  const prevHTML = prev
    ? `<a href="${prev.href}" class="prev"><span class="pn-label">上一页</span><span class="pn-title">${escapeHTML(prev.label)}</span></a>`
    : `<span class="prev placeholder"></span>`;
  const nextHTML = next
    ? `<a href="${next.href}" class="next"><span class="pn-label">下一页</span><span class="pn-title">${escapeHTML(next.label)}</span></a>`
    : `<span class="next placeholder"></span>`;
  return prevHTML + nextHTML;
}

function renderFooter() {
  const inner = SITE.footer.map((s) => `<span>${escapeHTML(s)}</span>`).join('');
  return `<footer class="footer">${inner}</footer>`;
}

async function mount() {
  await loadSiteConfig();

  const topMount = document.querySelector('[data-mount="topbar"]');
  if (topMount) topMount.outerHTML = renderTopbar();

  const sideMount = document.querySelector('[data-mount="sidebar"]');
  if (sideMount) sideMount.innerHTML = renderSidebar();

  const crumbMount = document.querySelector('[data-mount="breadcrumb"]');
  if (crumbMount) crumbMount.innerHTML = renderBreadcrumb();

  const pnMount = document.querySelector('[data-mount="prevnext"]');
  if (pnMount) pnMount.innerHTML = renderPrevNext();

  const footMount = document.querySelector('[data-mount="footer"]');
  if (footMount) footMount.outerHTML = renderFooter();

  const menuButton = document.querySelector('.menu-button');
  if (menuButton) {
    menuButton.addEventListener('click', () => {
      const isOpen = document.body.classList.toggle('sidebar-open');
      menuButton.setAttribute('aria-expanded', String(isOpen));
    });
  }
  document.querySelectorAll('.sidebar a').forEach((link) => {
    link.addEventListener('click', () => {
      document.body.classList.remove('sidebar-open');
    });
  });

  initTabs();
  initConstants();
  initApiPlayground();
}

function initConstants() {
  document.querySelectorAll('[data-api-base]:not([data-playground])').forEach((node) => {
    node.textContent = API_BASE_URL;
  });
  document.querySelectorAll('[data-seedance-api-base]').forEach((node) => {
    node.textContent = SEEDANCE_API_BASE_URL || '尚未配置';
  });
}

// 「试一试」交互面板：折叠/展开请求表单，向同源接口发送真实请求并展示响应。
function initApiPlayground() {
  document.querySelectorAll('[data-playground]').forEach((root) => {
    const method = (root.getAttribute('data-method') || 'POST').toUpperCase();
    const path = root.getAttribute('data-path') || '';
    const selectedApiBase = root.getAttribute('data-api-base');
    const apiBase = selectedApiBase === 'seedance-proxy'
      ? (SEEDANCE_API_BASE_URL ? location.origin : '')
      : selectedApiBase === 'seedance'
        ? SEEDANCE_API_BASE_URL
        : API_BASE_URL;
    const encoding = (root.getAttribute('data-encoding') || 'json').toLowerCase();
    const authMode = (root.getAttribute('data-auth') || 'bearer').toLowerCase();
    const tryBtn = root.querySelector('.api-try-btn');
    const tryPanel = root.querySelector('.api-try-panel');
    if (!tryBtn || !tryPanel) return;

    const tokenInput = tryPanel.querySelector('[data-field="token"]');
    const actionInput = tryPanel.querySelector('[data-field="action"]');
    const bodyInput = tryPanel.querySelector('[data-field="body"]');
    const sendBtn = tryPanel.querySelector('.api-send');
    const responseBox = tryPanel.querySelector('.api-response');
    const responseLink = tryPanel.querySelector('.api-response-link');
    const jsonInputs = Array.from(tryPanel.querySelectorAll('[data-json-path]'));

    const readJsonBody = () => {
      if (!bodyInput) return null;
      const raw = bodyInput.value.trim();
      if (!raw) return {};
      try {
        return JSON.parse(raw);
      } catch {
        return null;
      }
    };

    const jsonInputPaths = (input) => (input.getAttribute('data-json-path') || '')
      .split(',')
      .map((path) => path.trim())
      .filter(Boolean);

    const jsonInputValue = (input) => input.type === 'checkbox' ? input.checked : input.value;

    const syncControlsToBody = () => {
      const payload = readJsonBody();
      if (payload === null) return;
      jsonInputs.forEach((input) => {
        const paths = jsonInputPaths(input);
        if (!paths.length) return;
        const rawValue = jsonInputValue(input);
        if (input.hasAttribute('data-json-omit-empty') && typeof rawValue === 'string' && !rawValue.trim()) {
          paths.forEach((jsonPath) => deleteJsonPath(payload, jsonPath));
          return;
        }
        const value = coerceJsonFieldValue(rawValue, input.getAttribute('data-json-type'));
        paths.forEach((jsonPath) => setJsonPath(payload, jsonPath, value));
      });
      bodyInput.value = JSON.stringify(payload, null, 2);
    };

    const syncBodyToControls = () => {
      const payload = readJsonBody();
      if (payload === null) return;
      jsonInputs.forEach((input) => {
        const paths = jsonInputPaths(input);
        const value = paths.map((jsonPath) => getJsonPath(payload, jsonPath))
          .find((candidate) => candidate !== undefined);
        if (value === undefined) return;
        if (input.type === 'checkbox') input.checked = Boolean(value);
        else input.value = String(value);
      });
    };

    tryBtn.addEventListener('click', () => {
      const willShow = tryPanel.hasAttribute('hidden');
      if (willShow) tryPanel.removeAttribute('hidden');
      else tryPanel.setAttribute('hidden', '');
      tryBtn.textContent = willShow ? '收起' : '试一试';
    });

    if (actionInput && bodyInput) {
      const updateAction = () => {
        const action = actionInput.value;
        const template = ASSET_ACTION_TEMPLATES[action];
        if (template) bodyInput.value = JSON.stringify(template, null, 2);
        const pathNode = root.querySelector('.api-path');
        if (pathNode) pathNode.textContent = `/api/material?Action=${action}`;
      };
      actionInput.addEventListener('change', updateAction);
      document.querySelectorAll('[data-asset-try-action]').forEach((button) => {
        button.addEventListener('click', () => {
          const action = button.getAttribute('data-asset-try-action') || '';
          if (!ASSET_ACTION_TEMPLATES[action]) return;
          actionInput.value = action;
          updateAction();
          tryPanel.removeAttribute('hidden');
          tryBtn.textContent = '收起';
          root.scrollIntoView({ behavior: 'smooth', block: 'start' });
        });
      });
      updateAction();
    }

    if (bodyInput && jsonInputs.length) {
      jsonInputs.forEach((input) => {
        input.addEventListener('input', syncControlsToBody);
        input.addEventListener('change', syncControlsToBody);
      });
      bodyInput.addEventListener('input', syncBodyToControls);
      syncBodyToControls();
    }

    if (sendBtn) {
      sendBtn.addEventListener('click', async () => {
        const token = (tokenInput && tokenInput.value || '').trim();
        if (authMode === 'bearer' && !token) {
          if (responseBox) responseBox.textContent = '请先填写 API 令牌（sk-...）。';
          return;
        }
        let requestPath = path;
        const missingPathField = [];
        requestPath = requestPath.replace(/\{([^}]+)\}/g, (_, key) => {
          const input = tryPanel.querySelector(`[data-field="${key}"]`);
          const value = input && input.value ? input.value.trim() : '';
          if (!value) missingPathField.push(key);
          return encodeURIComponent(value);
        });
        if (missingPathField.length) {
          if (responseBox) responseBox.textContent = `请先填写路径参数：${missingPathField.join(', ')}。`;
          return;
        }

        if (!apiBase) {
          if (responseBox) responseBox.textContent = '尚未配置 Seedance API URL，请联系管理员。';
          return;
        }
        const url = apiBase.replace(/\/+$/, '') + requestPath;
        const init = {
          method,
          headers: authMode === 'bearer' ? { Authorization: `Bearer ${token}` } : {},
        };
        if (method !== 'GET') {
          if (encoding === 'multipart') {
            const form = new FormData();
            const missingFields = [];
            tryPanel.querySelectorAll('[data-form-field]').forEach((input) => {
              const field = input.getAttribute('data-form-field');
              if (!field) return;
              const required = input.hasAttribute('data-required');
              if (input.type === 'file') {
                const files = Array.from(input.files || []);
                if (required && files.length === 0) missingFields.push(field);
                files.forEach((file) => form.append(field, file, file.name));
                return;
              }
              const value = input.type === 'checkbox'
                ? String(input.checked)
                : String(input.value || '').trim();
              if (required && !value) missingFields.push(field);
              if (value) form.append(field, value);
            });
            if (missingFields.length) {
              if (responseBox) responseBox.textContent = `请先填写必填参数：${missingFields.join(', ')}。`;
              return;
            }
            init.body = form;
          } else if (bodyInput) {
            init.headers['Content-Type'] = 'application/json';
            const raw = bodyInput.value.trim();
            if (raw) {
              let payload;
              try {
                payload = JSON.parse(raw);
              } catch (e) {
                if (responseBox) responseBox.textContent = `请求体不是合法 JSON：${e.message}`;
                return;
              }
              jsonInputs.forEach((input) => {
                const paths = jsonInputPaths(input);
                if (!paths.length) return;
                const rawValue = jsonInputValue(input);
                if (input.hasAttribute('data-json-omit-empty') && typeof rawValue === 'string' && !rawValue.trim()) {
                  paths.forEach((path) => deleteJsonPath(payload, path));
                  return;
                }
                const value = coerceJsonFieldValue(rawValue, input.getAttribute('data-json-type'));
                paths.forEach((path) => setJsonPath(payload, path, value));
              });
              init.body = JSON.stringify(payload);
              bodyInput.value = JSON.stringify(payload, null, 2);
            }
          }
        }

        sendBtn.disabled = true;
        const original = sendBtn.textContent;
        sendBtn.textContent = '发送中…';
        if (responseBox) responseBox.textContent = '';
        if (responseLink) {
          responseLink.hidden = true;
          responseLink.textContent = '';
        }
        try {
          let requestURL = url;
          if (actionInput) {
            const action = String(actionInput && actionInput.value || '').trim();
            if (!action) throw new Error('请选择素材 Action');
            const separator = requestURL.includes('?') ? '&' : '?';
            requestURL = `${requestURL}${separator}Action=${encodeURIComponent(action)}`;
          }
          const res = await fetch(requestURL, init);
          const text = await res.text();
          let parsedResponse = null;
          try {
            parsedResponse = JSON.parse(text);
          } catch {
            // 非 JSON 响应直接展示原文
          }
          let pretty = text;
          if (parsedResponse !== null) pretty = JSON.stringify(parsedResponse, null, 2);
          if (pretty.length > 24000) {
            pretty = `${pretty.slice(0, 24000)}\n\n...响应内容过长，已截断显示。`;
          }
          if (responseBox) responseBox.textContent = `HTTP ${res.status} ${res.statusText}\n\n${pretty}`;
          const h5Link = parsedResponse && parsedResponse.Result && parsedResponse.Result.H5Link;
          if (responseLink && actionInput && actionInput.value === 'CreateRealValidateH5' && typeof h5Link === 'string' && /^https?:\/\//i.test(h5Link)) {
            const expiresIn = Number(parsedResponse.Result.ExpiresIn);
            const expiresText = Number.isFinite(expiresIn) && expiresIn > 0 ? `（有效期 ${expiresIn} 秒）` : '（按服务端返回状态有效）';
            responseLink.innerHTML = `<a href="${escapeHTML(h5Link)}" target="_blank" rel="noopener noreferrer">打开真人认证页面</a> <span>${expiresText}</span>`;
            responseLink.hidden = false;
          }
        } catch (err) {
          if (responseBox) responseBox.textContent = `请求失败：${err.message}`;
        } finally {
          sendBtn.disabled = false;
          sendBtn.textContent = original;
        }
      });
    }
  });
}

function coerceJsonFieldValue(value, type) {
  if (type === 'number') {
    const n = Number(value);
    return Number.isFinite(n) ? n : value;
  }
  if (type === 'boolean') {
    return value === true || value === 'true' || value === '1';
  }
  return value;
}

function setJsonPath(target, path, value) {
  const parts = path.split('.');
  let current = target;
  for (let i = 0; i < parts.length - 1; i += 1) {
    const key = parts[i];
    const nextKey = parts[i + 1];
    const isNextIndex = /^\d+$/.test(nextKey);
    if (/^\d+$/.test(key)) {
      const idx = Number(key);
      if (current[idx] === undefined || current[idx] === null) current[idx] = isNextIndex ? [] : {};
      current = current[idx];
      continue;
    }
    if (current[key] === undefined || current[key] === null) current[key] = isNextIndex ? [] : {};
    current = current[key];
  }
  const last = parts[parts.length - 1];
  if (/^\d+$/.test(last)) current[Number(last)] = value;
  else current[last] = value;
}

function getJsonPath(target, path) {
  return path.split('.').reduce((current, key) => {
    if (current === undefined || current === null) return undefined;
    const normalizedKey = /^\d+$/.test(key) ? Number(key) : key;
    return current[normalizedKey];
  }, target);
}

function deleteJsonPath(target, path) {
  const parts = path.split('.');
  let current = target;
  for (let i = 0; i < parts.length - 1; i += 1) {
    if (current === undefined || current === null) return;
    const key = /^\d+$/.test(parts[i]) ? Number(parts[i]) : parts[i];
    current = current[key];
  }
  if (current === undefined || current === null) return;
  const last = parts[parts.length - 1];
  if (Array.isArray(current) && /^\d+$/.test(last)) current.splice(Number(last), 1);
  else delete current[last];
}

function initTabs() {
  document.querySelectorAll('[data-tabs]').forEach((tabs) => {
    const buttons = tabs.querySelectorAll(':scope > .tabs-nav > button');
    const panels = tabs.querySelectorAll(':scope > .tab-panel');
    buttons.forEach((btn, i) => {
      btn.addEventListener('click', () => {
        buttons.forEach((b) => b.classList.remove('active'));
        panels.forEach((p) => p.classList.remove('active'));
        btn.classList.add('active');
        if (panels[i]) panels[i].classList.add('active');
      });
    });
  });

  document.querySelectorAll('[data-vp-tabs]').forEach((tabs) => {
    const buttons = tabs.querySelectorAll(':scope > .vp-tabs-nav > .vp-tab-nav');
    const panels = tabs.querySelectorAll(':scope > .vp-tab');
    buttons.forEach((btn, i) => {
      btn.addEventListener('click', () => {
        buttons.forEach((b) => {
          b.classList.remove('active');
          b.setAttribute('aria-selected', 'false');
        });
        panels.forEach((p) => {
          p.classList.remove('active');
          p.setAttribute('aria-expanded', 'false');
        });
        btn.classList.add('active');
        btn.setAttribute('aria-selected', 'true');
        if (panels[i]) {
          panels[i].classList.add('active');
          panels[i].setAttribute('aria-expanded', 'true');
        }
      });
    });
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', mount);
} else {
  mount();
}
