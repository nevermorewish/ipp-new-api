const MODELS = {
  "doubao-seedance-2.0": ["480p", "720p", "1080p", "4k"],
  "doubao-seedance-2.0-fast": ["480p", "720p"],
  "doubao-seedance-2.0-mini": ["480p", "720p"],
  "doubao-seedance-2.5": ["480p", "720p", "1080p"],
};

function usageSchema(resolutions) {
  return {
    tokens: { type: "number", unit: "token", description: { en: "Billing token unit price", zh: "计费 Token 单价" } },
    resolution: { enum: resolutions, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
    video_input: {
      enum: ["none", "video"],
      description: { en: "Reference video input", zh: "参考视频输入" },
      enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } },
    },
  };
}

export const meta = {
  apiVersion: 1,
  key: "seedanceapi",
  name: "SeedanceAPI",
  icon: "Doubao.Color",
  version: "1.0.0",
  author: { name: "QuantumNous" },
  description: { en: "SeedanceAPI video generation with signed tenant identities", zh: "使用租户身份签名的 SeedanceAPI 视频生成" },
  channelTypes: [66],
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  usageSchema: usageSchema(["480p", "720p", "1080p", "4k"]),
  usageExamples: [{ label: "720p · 5s", facts: { tokens: 108000, resolution: "720p", video_input: "none" } }],
  usageProfiles: Object.keys(MODELS).map((model) => ({
    models: [model],
    schema: usageSchema(MODELS[model]),
    examples: [{ label: "720p · 5s", facts: { tokens: 108000, resolution: "720p", video_input: "none" } }],
  })),
  routes: [
    { method: "POST", path: "/seedanceapi/api/v3/contents/generations/tasks", type: "submit", decode: "createTask", render: "taskCreated" },
    { method: "GET", path: "/seedanceapi/api/v3/contents/generations/tasks/:task_id", type: "query", render: "taskStatus" },
  ],
  protocols: ["openai_video"],
};

const INPUT_DURATION_FIELDS = ["input_video_seconds", "input_video_duration", "input_seconds", "input_duration", "video_seconds", "video_duration"];

function boundedSeconds(value, name) {
  const seconds = Number(value);
  // Same upper bound as the host's MaxTaskDurationSeconds. Validate every
  // alias, including metadata and multipart values, before reserving quota.
  if (
    (typeof value !== "number" && typeof value !== "string") ||
    !String(value).trim() ||
    !Number.isFinite(seconds) ||
    !Number.isInteger(seconds) ||
    seconds < 1 ||
    seconds > 3600
  )
    throw new Error(name + " must be an integer between 1 and 3600");
  return seconds;
}

function videoInput(req) {
  if (Array.isArray(req.reference_videos) && req.reference_videos.length) return true;
  if (Array.isArray(req.referenceVideos) && req.referenceVideos.length) return true;
  if (Array.isArray(req.content) && req.content.some((item) => item && (item.type === "video_url" || item.video_url))) return true;
  const metadata = req.metadata || {};
  if (Array.isArray(metadata.content) && metadata.content.some((item) => item && (item.type === "video_url" || item.video_url))) return true;
  if (Array.isArray(metadata.referenceVideos) && metadata.referenceVideos.length) return true;
  if (metadata.video_url || metadata.reference_video || (Array.isArray(metadata.reference_videos) && metadata.reference_videos.length)) return true;
  const candidates = [req.input_reference, req.image].concat(req.images || [], req.image_urls || []);
  return candidates.some(
    (value) =>
      (value && typeof value === "object" && /^video\//i.test(value.mimeType || "")) ||
      (typeof value === "string" && (/^data:video\//i.test(value) || /\.(mp4|mov|webm|m4v|avi|mkv)(?:[?#]|$)/i.test(value)))
  );
}

function requestResolution(req) {
  const metadata = req.metadata || {};
  let resolution = String(req.resolution || metadata.resolution || "")
    .trim()
    .toLowerCase();
  if (!resolution) {
    const size = String(req.size || "").toLowerCase();
    if (["480p", "720p", "1080p", "2160p", "4k"].includes(size)) resolution = size;
    else {
      const dimensions = /^(\d+)[x*](\d+)$/.exec(size);
      if (dimensions) {
        const shortSide = Math.min(Number(dimensions[1]), Number(dimensions[2]));
        if (shortSide >= 2160) resolution = "4k";
        else if (shortSide >= 1000) resolution = "1080p";
        else if (shortSide >= 700) resolution = "720p";
        else resolution = "480p";
      } else if (size) throw new Error("unsupported video size");
    }
  }
  if (resolution === "2160p") return "4k";
  return resolution || "720p";
}

function validateRequest(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
  if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
    throw new Error("metadata must be an object");
  for (const container of [req, req.metadata || {}]) {
    for (const key of ["seconds", "duration"].concat(INPUT_DURATION_FIELDS)) {
      if (container[key] !== undefined) boundedSeconds(container[key], key);
    }
  }
  for (const aliases of [["seconds", "duration"], INPUT_DURATION_FIELDS]) {
    const values = [];
    for (const container of [req, req.metadata || {}]) {
      for (const key of aliases) if (container[key] !== undefined) values.push(Number(container[key]));
    }
    if (values.some((value) => value !== values[0])) throw new Error("conflicting duration parameters");
  }
  for (const container of [req, req.metadata || {}]) {
    if (container.n !== undefined && boundedSeconds(container.n, "n") !== 1) throw new Error("only one video per request is supported");
    for (const key of ["resolution", "size"]) {
      if (container[key] !== undefined && requestResolution({ [key]: container[key] }) !== requestResolution(req))
        throw new Error("conflicting resolution parameters");
    }
  }
  const resolutions = MODELS[model] || ["480p", "720p", "1080p", "4k"];
  if (!resolutions.includes(requestResolution(req))) throw new Error("unsupported resolution for " + model);
  if (!String(req.prompt || "").trim() && !(Array.isArray(req.content) && req.content.length)) throw new Error("prompt or content is required");
}

function decodeRequest(ctx) {
  if (!ctx.body || !["json", "multipart"].includes(ctx.body.kind)) throw new Error("JSON or multipart body required");
  let req;
  if (ctx.body.kind === "json") {
    if (!ctx.body.value || typeof ctx.body.value !== "object" || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
    req = Object.assign({}, ctx.body.value);
  } else {
    req = {};
    for (const name of Object.keys(ctx.body.fields || {})) {
      const values = ctx.body.fields[name];
      if (values.length !== 1) throw new Error(name + " must be provided once");
      req[name] = values[0];
    }
    for (const name of ["metadata", "content", "images", "image_urls", "reference_videos", "referenceImages", "referenceVideos", "referenceAudios"]) {
      if (typeof req[name] === "string") req[name] = JSON.parse(req[name]);
    }
    for (const file of ctx.body.files || []) {
      const listField = ["images", "referenceImages", "referenceVideos", "referenceAudios"].includes(file.field);
      if (!listField && file.field !== "input_reference" && file.field !== "image") throw new Error("unsupported file field");
      const value = { __fileRef: file.ref, encoding: "dataUrl", mimeType: file.mimeType || "application/octet-stream" };
      if (listField) {
        if (req[file.field] === undefined) req[file.field] = [];
        if (!Array.isArray(req[file.field])) throw new Error(file.field + " must be an array");
        req[file.field].push(value);
      } else {
        if (req[file.field] !== undefined) throw new Error(file.field + " must be provided once");
        req[file.field] = value;
      }
    }
    if (req.seconds !== undefined) {
      if (req.duration === undefined) req.duration = boundedSeconds(req.seconds, "seconds");
      else if (boundedSeconds(req.seconds, "seconds") !== boundedSeconds(req.duration, "duration")) throw new Error("conflicting duration parameters");
      delete req.seconds;
    }
    if (req.duration !== undefined) req.duration = boundedSeconds(req.duration, "duration");
    for (const key of ["watermark", "generate_audio", "return_last_frame"]) {
      if (req[key] !== undefined) {
        if (req[key] !== "true" && req[key] !== "false") throw new Error(key + " must be true or false");
        req[key] = req[key] === "true";
      }
    }
  }
  const model = String(ctx.model || req.model || "").trim();
  if (!model) throw new Error("model is required");
  validateRequest(req, ctx.upstreamModel || model);
  req.model = model;
  return { kind: "submit", model, action: videoInput(req) ? "video_to_video" : "text_to_video", requestBody: req };
}

function upstreamHeaders(ctx) {
  if (ctx.upstream && ctx.upstream.kind === "new_api") return { Authorization: ctx.authHeader };
  if (ctx.authError || !ctx.auth || !ctx.auth.headers) throw new Error("SeedanceAPI signed identity is unavailable");
  return Object.assign({}, ctx.auth.headers);
}

export function buildSubmitRequest(ctx) {
  const req = Object.assign({}, ctx.requestBody, { model: ctx.upstreamModel || ctx.model });
  validateRequest(req, req.model);
  const ark = String(ctx.path || "").includes("/api/v3/contents/generations") || Array.isArray(req.content);
  let path = ark ? "/api/v3/contents/generations/tasks" : ctx.path === "/v1/video/generations" ? ctx.path : "/v1/videos";
  if (ark && ctx.upstream && ctx.upstream.kind === "new_api") path = "/seedanceapi" + path;
  const headers = upstreamHeaders(ctx);
  headers["Content-Type"] = "application/json";
  return { method: "POST", url: ctx.baseUrl.replace(/\/+$/, "") + path, headers, body: req };
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body || {};
  const taskId = body.id || body.task_id;
  if (typeof taskId !== "string" || !taskId) throw new Error("upstream task id is missing");
  return {
    taskId,
    taskData: body,
    state: {
      identity: (ctx.auth || {}).identity || null,
      ark: String(ctx.path || "").includes("/api/v3/contents/generations") || Array.isArray((ctx.requestBody || {}).content),
    },
  };
}

export function extractUsage(ctx) {
  // Legacy per-call prices remain per call; token facts belong exclusively to
  // expression pricing and must never become multiplicative price ratios.
  if (ctx.usagePurpose === "billing_ratios") return null;
  const req = ctx.requestBody || {};
  const model = ctx.upstreamModel || ctx.model;
  validateRequest(req, model);
  const metadata = req.metadata || {};
  const seconds = boundedSeconds(req.seconds ?? req.duration ?? metadata.seconds ?? metadata.duration ?? 4, "duration");
  const hasVideo = videoInput(req);
  let inputSeconds = 0;
  if (hasVideo) {
    inputSeconds = 4;
    for (const key of INPUT_DURATION_FIELDS) {
      if (req[key] !== undefined || metadata[key] !== undefined) {
        inputSeconds = boundedSeconds(req[key] ?? metadata[key], key);
        break;
      }
    }
    let minimum = seconds - 1;
    if (String(model).startsWith("doubao-seedance-2.0") && seconds >= 10) minimum = seconds - 3;
    inputSeconds = Math.max(inputSeconds, minimum);
  }
  const resolution = requestResolution(req);
  const tokenRates = { "480p": 10044, "720p": 21600, "1080p": 48600, "4k": 194400 };
  if (model === "doubao-seedance-2.5") tokenRates["480p"] = 9607.5;
  return { tokens: Math.round((seconds + inputSeconds) * tokenRates[resolution]), resolution, video_input: hasVideo ? "video" : "none" };
}

export function buildQueryRequest(ctx) {
  let path = ctx.state && ctx.state.ark ? "/api/v3/contents/generations/tasks/" : "/v1/videos/";
  if (ctx.state && ctx.state.ark && ctx.upstream && ctx.upstream.kind === "new_api") path = "/seedanceapi" + path;
  return { method: "GET", url: ctx.baseUrl.replace(/\/+$/, "") + path + encodeURIComponent(ctx.taskId), headers: upstreamHeaders(ctx) };
}

export function parseTaskResult(_ctx, body) {
  const statuses = {
    queued: "QUEUED",
    pending: "QUEUED",
    submitted: "QUEUED",
    running: "IN_PROGRESS",
    processing: "IN_PROGRESS",
    in_progress: "IN_PROGRESS",
    succeeded: "SUCCESS",
    completed: "SUCCESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
    expired: "FAILURE",
  };
  const status = statuses[String(body.status || "").toLowerCase()] || "UNKNOWN";
  let progress = Number(body.progress);
  if (!Number.isFinite(progress)) progress = 0;
  if (status === "SUCCESS") progress = 100;
  return {
    taskId: body.id || body.task_id,
    status,
    progress: Math.max(0, Math.min(progress, 100)) + "%",
    reason: (body.error || {}).message || body.fail_reason || "",
  };
}

export function extractUsageOnComplete(task, result, body) {
  if (result.status !== "SUCCESS") return {};
  const usage = body.usage || {};
  const facts = {};
  const rawTokens = usage.completion_tokens ?? usage.total_tokens;
  const tokens = Number(rawTokens);
  if (
    (typeof rawTokens === "number" || (typeof rawTokens === "string" && rawTokens.trim())) &&
    Number.isFinite(tokens) &&
    Number.isInteger(tokens) &&
    tokens >= 0 &&
    tokens <= 2147483647
  )
    facts.tokens = tokens;
  const resolution = String(body.resolution || (body.content || {}).resolution || "").toLowerCase();
  if ((MODELS[task.upstreamModel || task.model] || []).includes(resolution)) facts.resolution = resolution;
  return facts;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  return {
    method: ctx.clientRequest.method,
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    headers: upstreamHeaders(ctx),
  };
}

function publicVideoResponse(task) {
  const body = Object.assign({}, task.data || {}, { id: task.task_id });
  delete body.task_id;
  const direct = (body.metadata || {}).url;
  const content = Object.assign({}, body.content || {});
  if (typeof direct === "string" && /^https?:\/\//i.test(direct) && /\.mp4(?:[?#]|$)/i.test(direct)) content.video_url = direct;
  else if (typeof content.video_url === "string" && /\/v1\/videos\/[^/]+\/content(?:[?#]|$)/.test(content.video_url))
    content.video_url = "/v1/videos/" + encodeURIComponent(task.task_id) + "/content";
  if (body.content || content.video_url) body.content = content;
  return body;
}

export const native = {
  createTask: decodeRequest,
  queryTask: function (ctx) {
    const taskId = (ctx.query.task_id || ctx.query.id || [])[0];
    if (!taskId) throw new Error("task_id is required");
    return { kind: "query", taskId };
  },
  taskCreated: function (_ctx, task) {
    return { id: task.task_id };
  },
  taskStatus: function (_ctx, task) {
    const statuses = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "running", SUCCESS: "succeeded", FAILURE: "failed" };
    return Object.assign(publicVideoResponse(task), { status: statuses[task.status] || "unknown" });
  },
};

export const protocols = {
  openai_video: {
    decodeRequest,
    render: function (_ctx, task) {
      return publicVideoResponse(task);
    },
  },
};
