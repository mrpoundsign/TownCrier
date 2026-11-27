(function(global) {
  'use strict';

  function parsePluginUUID(pathname) {
    const m = /^\/plugins\/([^/]+)(?:\/|$)/.exec(String(pathname || ''));
    return m ? decodeURIComponent(m[1]) : '';
  }

  function ensureStyleTag(id) {
    let el = document.getElementById(id);
    if (!el) {
      el = document.createElement('style');
      el.id = id;
      (document.head || document.documentElement).appendChild(el);
    }
    return el;
  }

  function applyStyleAndTheme(styleObj, hostTheme) {
    const style = styleObj || {};
    const useHost = !!style.use_host_theme;
    const root = document.documentElement;
    if (root) {
      if (useHost) {
        root.setAttribute('data-tc-host-theme', '1');
      } else {
        root.removeAttribute('data-tc-host-theme');
      }
    }

    const themeTag = ensureStyleTag('tc-host-theme-vars');
    if (hostTheme && typeof hostTheme === 'object') {
      const lines = [':root {'];
      Object.keys(hostTheme).forEach(function(k) {
        if (/^--[A-Za-z0-9_-]+$/.test(k) && typeof hostTheme[k] === 'string') {
          lines.push('  ' + k + ': ' + hostTheme[k] + ';');
        }
      });
      lines.push('}');
      themeTag.textContent = lines.join('\n');
    } else {
      themeTag.textContent = '';
    }

    const customTag = ensureStyleTag('tc-plugin-custom-css');
    customTag.textContent = (style.custom_css && typeof style.custom_css === 'string') ? style.custom_css : '';
  }

  function connect(userOptions) {
    const opts = userOptions || {};
    const params = new URLSearchParams(window.location.search);
    const pluginUUID = String(opts.pluginUUID || parsePluginUUID(window.location.pathname) || '').trim();
    const widgetId = String(opts.widgetId || params.get('widget') || '').trim();
    const token = String(opts.token || params.get('token') || '').trim();
    const embedded = params.get('embedded') === '1';
    const preview = params.get('preview') === '1';

    if (document.documentElement) {
      document.documentElement.classList.toggle('tc-embedded', embedded);
      document.documentElement.classList.toggle('tc-preview', preview);
    }

    const q = new URLSearchParams();
    if (widgetId) q.set('widget', widgetId);
    if (token) q.set('token', token);
    const qStr = q.toString();
    const qSuffix = qStr ? ('?' + qStr) : '';

    const styleListeners = [];
    const eventListeners = [];
    if (typeof opts.onStyle === 'function') styleListeners.push(opts.onStyle);
    if (typeof opts.onEvent === 'function') eventListeners.push(opts.onEvent);

    let lastStyle = { values: {}, custom_css: '', use_host_theme: false };
    let lastHostTheme = {};

    function emitStyle(styleObj, hostTheme) {
      if (styleObj && typeof styleObj === 'object') {
        lastStyle = {
          values: Object.assign({}, styleObj.values || {}),
          custom_css: String(styleObj.custom_css || ''),
          use_host_theme: !!styleObj.use_host_theme
        };
      }
      if (hostTheme && typeof hostTheme === 'object') {
        lastHostTheme = Object.assign({}, hostTheme);
      }
      applyStyleAndTheme(lastStyle, lastHostTheme);
      styleListeners.forEach(function(fn) {
        try {
          fn(lastStyle, lastHostTheme);
        } catch (_) {}
      });
    }

    function dispatchPluginEvent(eventName, outerOrPayload) {
      let payload = outerOrPayload;
      if (outerOrPayload && typeof outerOrPayload === 'object' && Object.prototype.hasOwnProperty.call(outerOrPayload, 'payload')) {
        payload = outerOrPayload.payload;
      }
      eventListeners.forEach(function(fn) {
        try {
          fn(eventName, payload, outerOrPayload);
        } catch (_) {}
      });
    }

    if (pluginUUID) {
      fetch('/plugins/' + encodeURIComponent(pluginUUID) + '/widget-style' + qSuffix, { cache: 'no-store' })
        .then(function(r) { return r.ok ? r.json() : null; })
        .then(function(data) {
          if (data) emitStyle(data.style || {}, data.host_theme || {});
        })
        .catch(function() {});

      if (!preview || opts.connectInPreview) {
        fetch('/plugins/' + encodeURIComponent(pluginUUID) + '/state' + qSuffix, { cache: 'no-store' })
          .then(function(r) { return r.ok ? r.json() : null; })
          .then(function(data) {
            if (!data) return;
            if (widgetId && data.styles && data.styles[widgetId]) {
              emitStyle(data.styles[widgetId], data.host_theme || lastHostTheme);
            } else if (data.host_theme) {
              emitStyle(null, data.host_theme);
            }
            if (data.events && typeof data.events === 'object') {
              Object.keys(data.events).forEach(function(evName) {
                dispatchPluginEvent(evName, data.events[evName]);
              });
            }
          })
          .catch(function() {});
      }
    }

    let es = null;
    const namedBound = Object.create(null);

    function bindEventName(name) {
      if (!es || !name || namedBound[name]) return;
      namedBound[name] = true;
      es.addEventListener(name, function(ev) {
        try {
          const parsed = JSON.parse(ev.data);
          dispatchPluginEvent(name, parsed);
        } catch (_) {}
      });
    }

    if (pluginUUID && (!preview || opts.connectInPreview)) {
      es = new EventSource('/plugins/' + encodeURIComponent(pluginUUID) + '/events' + qSuffix);
      es.addEventListener('style', function(ev) {
        try {
          const parsed = JSON.parse(ev.data);
          emitStyle(parsed.style || {}, parsed.host_theme || lastHostTheme);
        } catch (_) {}
      });
      es.addEventListener('reload', function() {
        if (opts.reloadOnHostReload === true) {
          window.location.reload();
        }
      });
      es.onmessage = function(ev) {
        try {
          const parsed = JSON.parse(ev.data);
          dispatchPluginEvent((parsed && parsed.event_name) || '', parsed);
        } catch (_) {}
      };
      if (Array.isArray(opts.events)) {
        opts.events.forEach(bindEventName);
      }
    }

    window.addEventListener('message', function(ev) {
      if (ev.origin !== window.location.origin || !ev.data || ev.data.type !== 'tc:overlay:style') {
        return;
      }
      if (widgetId && ev.data.widget_id && ev.data.widget_id !== widgetId) {
        return;
      }
      emitStyle(ev.data.style || {}, ev.data.host_theme || lastHostTheme);
    });

    return {
      pluginUUID: pluginUUID,
      widgetId: widgetId,
      embedded: embedded,
      preview: preview,
      onStyle: function(fn) {
        if (typeof fn === 'function') {
          styleListeners.push(fn);
          fn(lastStyle, lastHostTheme);
        }
      },
      onEvent: function(fnOrName, maybeFn) {
        if (typeof fnOrName === 'string' && typeof maybeFn === 'function') {
          bindEventName(fnOrName);
          eventListeners.push(function(evName, payload, outer) {
            if (evName === fnOrName) maybeFn(payload, outer);
          });
        } else if (typeof fnOrName === 'function') {
          eventListeners.push(fnOrName);
        }
      },
      subscribeEvent: function(name) {
        bindEventName(name);
      },
      close: function() {
        if (es) {
          es.close();
          es = null;
        }
      }
    };
  }

  global.TownCrierOverlay = {
    connect: connect,
    parsePluginUUID: parsePluginUUID
  };
})(window);
