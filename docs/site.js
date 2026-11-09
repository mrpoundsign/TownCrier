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
