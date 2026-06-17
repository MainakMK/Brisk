-- Brisk edge Lua layer (Phase 4 Step 5). Enforces per-zone custom cache rules +
-- request/response header transforms. Loaded once (init_by_lua); the per-zone data
-- (zones_data.lua, rendered by brisk-agent) is reloaded on each config reload.
--
-- Everything per-request is pcall-wrapped: a Lua error falls back to DEFAULT
-- behavior (serve normally) and logs — a broken rule never blackholes a tenant.
local _M = {}

-- ZONES[host] = { cache_rules = {...}, req_headers = {...}, resp_headers = {...} }
local ZONES = {}

-- Headers Brisk manages itself — a tenant transform can never touch these
-- (defense in depth; the control plane API also rejects them).
local DENY = {
  ["server"] = true, ["strict-transport-security"] = true,
  ["content-length"] = true, ["transfer-encoding"] = true, ["connection"] = true,
  ["content-encoding"] = true, ["host"] = true,
}
local function denied(h)
  h = string.lower(h)
  if h:sub(1, 8) == "x-brisk-" then return true end
  return DENY[h] == true
end

-- load reads the agent-rendered Lua data file into ZONES. Called from init_by_lua
-- (master start + every reload). On error ZONES stays as-is/empty (fail-open).
function _M.load(path)
  local ok, data = pcall(dofile, path)
  if ok and type(data) == "table" then
    ZONES = data
  else
    ngx.log(ngx.ERR, "brisk: failed to load lua data ", path, ": ", tostring(data))
  end
end

local function current_zone()
  return ZONES[ngx.var.brisk_zone or ""]
end

-- match tests a rule/transform condition against the request path/method. Returns
-- (matched, captures) where captures is the regex capture array (for redirect $N).
local function match(rule, path, method)
  local mt = rule.match_type
  if mt == nil or mt == "all" then return true, nil end
  if mt == "path_prefix" then
    return path:sub(1, #rule.match_value) == rule.match_value, nil
  elseif mt == "extension" then
    local ext = path:match("%.([%w]+)$")
    return ext ~= nil and ext:lower() == rule.match_value:lower(), nil
  elseif mt == "regex" or mt == "path_regex" then
    local m = ngx.re.match(path, rule.match_value, "jo")
    return m ~= nil, m
  elseif mt == "method" then
    return method == rule.match_value:upper(), nil
  end
  return false, nil
end

-- expand substitutes $1..$9 in a redirect target from regex captures.
local function expand(target, caps)
  if not caps then return target end
  return (target:gsub("%$([1-9])", function(n)
    return caps[tonumber(n)] or ""
  end))
end

-- rewrite phase: cache rules (redirect / bypass / mark ttl|download, first match
-- wins) + request header transforms. Runs before WAF/access + cache lookup.
-- skip reports requests the Brisk Lua must NOT touch: internal subrequests (the
-- WAF auth_request, error_page fallbacks, internal redirects) and the health probe
-- (a redirect rule must never hijack /healthz or the WAF /_waf subrequest).
local function skip()
  if ngx.req.is_internal() then return true end
  local uri = ngx.var.uri
  return uri == "/healthz" or uri == "/_waf"
end

function _M.rewrite()
  local ok, err = pcall(function()
    if skip() then return end
    local z = current_zone()
    if not z then return end
    local path = ngx.var.uri or "/"
    local method = ngx.var.request_method or "GET"

    if z.cache_rules then
      for _, r in ipairs(z.cache_rules) do
        local m, caps = match(r, path, method)
        if m then
          local a = r.action
          if a == "redirect" then
            return ngx.redirect(expand(r.action_value or "/", caps), 301)
          elseif a == "bypass_cache" then
            -- Signal bypass via a REQUEST HEADER, not a writable nginx var: a var
            -- set here doesn't survive the WAF auth_request access phase, but a
            -- request header reliably reaches proxy_cache_bypass ($http_...).
            ngx.req.set_header("X-Brisk-Lua-Nocache", "1")
          elseif a == "override_cache_ttl" or a == "force_download" then
            ngx.ctx.brisk_rule = r -- applied in header_filter
          end
          break -- first match wins
        end
      end
    end

    if z.req_headers then
      for _, t in ipairs(z.req_headers) do
        if not denied(t.header) and match(t, path, method) then
          if t.op == "remove" then
            ngx.req.clear_header(t.header)
          else
            ngx.req.set_header(t.header, t.value)
          end
        end
      end
    end
  end)
  if not ok then ngx.log(ngx.ERR, "brisk: rewrite error (fail-open): ", tostring(err)) end
end

-- header_filter phase: override-ttl Cache-Control / force_download (from the cache
-- rule captured in rewrite) + response header transforms.
function _M.header_filter()
  local ok, err = pcall(function()
    if skip() then return end
    local z = current_zone()
    if not z then return end

    local r = ngx.ctx.brisk_rule
    if r then
      if r.action == "override_cache_ttl" then
        local ttl = tonumber(r.action_value) or 0
        ngx.header["Cache-Control"] = "public, max-age=" .. ttl
        ngx.header["Expires"] = ngx.http_time(ngx.time() + ttl)
      elseif r.action == "force_download" then
        ngx.header["Content-Disposition"] = "attachment"
      end
    end

    if z.resp_headers then
      local path = ngx.var.uri or "/"
      local method = ngx.var.request_method or "GET"
      for _, t in ipairs(z.resp_headers) do
        if not denied(t.header) and match(t, path, method) then
          if t.op == "remove" then
            ngx.header[t.header] = nil
          else
            ngx.header[t.header] = t.value
          end
        end
      end
    end
  end)
  if not ok then ngx.log(ngx.ERR, "brisk: header_filter error (fail-open): ", tostring(err)) end
end

-- ---- errors-only rate limiting (Phase 4 Step 6 Part 5) ----------------------
-- Count ONLY error responses (401/403) toward a per-IP limit on login/OTP paths,
-- so a legitimate user typing a long password isn't throttled but a brute-forcer
-- is. nginx limit_req can't do this (it runs before the response is known), so the
-- count happens in the log phase and the block in the access phase, via a shared
-- dict (per-edge/approximate, like the native limits). Fail-open: any error here
-- (incl. the dict being absent) just lets the request through.
local ERROR_STATUSES = { [401] = true, [403] = true }

local function rl_match(rl, path)
  if rl.match_type == "prefix" then
    return path:sub(1, #rl.path_match) == rl.path_match
  end
  return path == rl.path_match -- exact (default)
end

local function rl_key(rl, path)
  local ip = ngx.var.remote_addr or "0"
  if rl.key == "ip_path" then
    return rl.id .. "|" .. ip .. "|" .. path
  end
  return rl.id .. "|" .. ip
end

-- access phase: reject (429) when this IP is already over an errors-only limit.
function _M.access()
  local dict = ngx.shared.brisk_rl
  if not dict then return end
  local ok, err = pcall(function()
    if skip() then return end
    local z = current_zone()
    if not z or not z.rate_limits then return end
    local path = ngx.var.uri or "/"
    for _, rl in ipairs(z.rate_limits) do
      if rl_match(rl, path) then
        local n = dict:get(rl_key(rl, path)) or 0
        if n >= rl.requests then
          return ngx.exit(429)
        end
      end
    end
  end)
  if not ok then ngx.log(ngx.ERR, "brisk: access rl error (fail-open): ", tostring(err)) end
end

-- log phase: on a 401/403, bump this IP's counter for any matching errors-only
-- limit. First hit sets the window TTL (= period); the window then expires.
function _M.log()
  local dict = ngx.shared.brisk_rl
  if not dict then return end
  local ok, err = pcall(function()
    if ngx.req.is_internal() then return end
    if not ERROR_STATUSES[ngx.status] then return end
    local z = current_zone()
    if not z or not z.rate_limits then return end
    local path = ngx.var.uri or "/"
    for _, rl in ipairs(z.rate_limits) do
      if rl_match(rl, path) then
        local k = rl_key(rl, path)
        local newv, e2 = dict:incr(k, 1)
        if not newv then -- key absent/expired: start a fresh window
          dict:set(k, 1, rl.period)
        end
        if e2 then ngx.log(ngx.ERR, "brisk: rl incr: ", tostring(e2)) end
      end
    end
  end)
  if not ok then ngx.log(ngx.ERR, "brisk: log rl error (fail-open): ", tostring(err)) end
end

return _M
