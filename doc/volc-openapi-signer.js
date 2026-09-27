(function attachVolcOpenApiSigner(global) {
  'use strict';

  const encoder = new TextEncoder();
  const algorithm = 'HMAC-SHA256';
  const region = 'cn-beijing';
  const service = 'ark';
  const terminal = 'request';
  const signedHeaders = 'host;x-content-sha256;x-date';

  function toHex(bytes) {
    return Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
  }

  function uriEscape(value) {
    return encodeURIComponent(String(value)).replace(/[!'()*]/g, (char) =>
      `%${char.charCodeAt(0).toString(16).toUpperCase()}`,
    );
  }

  async function sha256(value) {
    return new Uint8Array(await global.crypto.subtle.digest('SHA-256', encoder.encode(value)));
  }

  async function hmac(key, value) {
    const rawKey = typeof key === 'string' ? encoder.encode(key) : key;
    const cryptoKey = await global.crypto.subtle.importKey(
      'raw',
      rawKey,
      { name: 'HMAC', hash: 'SHA-256' },
      false,
      ['sign'],
    );
    return new Uint8Array(await global.crypto.subtle.sign('HMAC', cryptoKey, encoder.encode(value)));
  }

  function formatDate(value) {
    const date = value instanceof Date ? value : value ? new Date(value) : new Date();
    if (Number.isNaN(date.getTime())) throw new Error('签名时间无效');
    return date.toISOString().replace(/[:\-]|\.\d{3}/g, '');
  }

  async function sign(input) {
    if (!global.crypto || !global.crypto.subtle) throw new Error('当前浏览器不支持 Web Crypto，请使用 HTTPS 打开文档');

    const accessKeyId = String(input.accessKeyId || '').trim();
    const secretKey = String(input.secretKey || '').trim();
    const host = String(input.host || '').trim().toLowerCase();
    const action = String(input.action || '').trim();
    const body = typeof input.body === 'string' ? input.body : JSON.stringify(input.body || {});
    if (!accessKeyId || !secretKey || !host || !action) throw new Error('Access Key、Secret Key、Host 和 Action 均不能为空');

    const xDate = formatDate(input.date);
    const shortDate = xDate.slice(0, 8);
    const scope = `${shortDate}/${region}/${service}/${terminal}`;
    const contentSha256 = toHex(await sha256(body));
    const query = `Action=${uriEscape(action)}&Version=2024-01-01`;
    const canonicalHeaders = `host:${host}\nx-content-sha256:${contentSha256}\nx-date:${xDate}`;
    const canonicalRequest = `POST\n/\n${query}\n${canonicalHeaders}\n\n${signedHeaders}\n${contentSha256}`;
    const stringToSign = `${algorithm}\n${xDate}\n${scope}\n${toHex(await sha256(canonicalRequest))}`;

    const kDate = await hmac(secretKey, shortDate);
    const kRegion = await hmac(kDate, region);
    const kService = await hmac(kRegion, service);
    const signingKey = await hmac(kService, terminal);
    const signature = toHex(await hmac(signingKey, stringToSign));

    return {
      query,
      headers: {
        Authorization: `${algorithm} Credential=${accessKeyId}/${scope}, SignedHeaders=${signedHeaders}, Signature=${signature}`,
        'X-Date': xDate,
        'X-Content-Sha256': contentSha256,
      },
    };
  }

  global.VolcOpenApiSigner = { sign };
})(typeof window === 'undefined' ? globalThis : window);
