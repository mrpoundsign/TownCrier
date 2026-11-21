(() => {
  const subnav = document.querySelector('.sidebar-subnav');
  if (!subnav) return;

  const navLinks = Array.from(subnav.querySelectorAll('a[href^="#"]'));
  if (navLinks.length === 0) return;

  if (typeof CSS !== 'undefined' && CSS.supports && CSS.supports('scroll-target-group: auto')) {
    const syncAriaCurrent = () => {
      const currentLink = subnav.querySelector('a:target-current');
      navLinks.forEach((link) => {
        if (link === currentLink) {
          link.setAttribute('aria-current', 'true');
        } else {
          link.removeAttribute('aria-current');
        }
      });
    };
    syncAriaCurrent();
    document.addEventListener('scrollend', syncAriaCurrent, { passive: true });
    return;
  }

  const sections = navLinks
    .map((link) => {
      const id = link.getAttribute('href').slice(1);
      return document.getElementById(id);
    })
    .filter(Boolean);

  if (sections.length === 0 || typeof IntersectionObserver === 'undefined') return;

  const setActiveId = (activeId) => {
    navLinks.forEach((link) => {
      const isActive = link.getAttribute('href') === `#${activeId}`;
      link.classList.toggle('target-current', isActive);
      if (isActive) {
        link.setAttribute('aria-current', 'true');
      } else {
        link.removeAttribute('aria-current');
      }
    });
  };

  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) {
          setActiveId(entry.target.id);
        }
      });
    },
    { rootMargin: '-20% 0px -65% 0px', threshold: 0 }
  );

  sections.forEach((section) => observer.observe(section));
})();

(() => {
  const timers = new WeakMap();
  let lastMessage = '';

  function report(button, ok) {
    const tip = button.nextElementSibling;
    if (!tip) return;
    clearTimeout(timers.get(button));
    button.removeAttribute('data-copied');
    button.removeAttribute('data-failed');
    let message = ok ? 'Copied' : 'Copy failed';
    if (message === lastMessage) {
      message += '\u00A0';
    }
    lastMessage = message;
    tip.textContent = '';
    void button.offsetWidth;
    button.setAttribute(ok ? 'data-copied' : 'data-failed', '');
    requestAnimationFrame(() => {
      tip.textContent = message;
    });
    timers.set(
      button,
      setTimeout(() => {
        button.removeAttribute('data-copied');
        button.removeAttribute('data-failed');
        tip.textContent = '';
      }, 2000)
    );
  }

  async function copy(button, target) {
    if (!target) return;
    const text = target.matches('input, textarea')
      ? target.value
      : target.innerText.trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      report(button, true);
    } catch {
      report(button, false);
    }
  }

  if ('CommandEvent' in window) {
    document.addEventListener(
      'command',
      (event) => {
        if (event.command && event.command.startsWith('--copy')) {
          copy(event.source, event.target);
        }
      },
      true
    );
  } else {
    document.addEventListener('click', (event) => {
      const button = event.target.closest('button[command^="--copy"]');
      if (!button) return;
      const targetId = button.getAttribute('commandfor');
      const target = targetId ? document.getElementById(targetId) : null;
      if (target) {
        copy(button, target);
      }
    });
  }
})();
