(() => {
    if (location.hostname !== 'changjiang.yuketang.cn' || !/^\/web\/?$/.test(location.pathname)) return;

    let viewport = document.querySelector('meta[name="viewport"]');
    if (!viewport) {
        viewport = document.createElement('meta');
        viewport.name = 'viewport';
        document.head.appendChild(viewport);
    }
    viewport.content = 'width=device-width, initial-scale=1, user-scalable=yes';

    // 官方页的 950px 外框与 416px 登录卡片会触发整页缩小，二维码也随之变小。
    let style = document.getElementById('rainclass-mobile-login');
    if (!style) {
        style = document.createElement('style');
        style.id = 'rainclass-mobile-login';
        document.head.appendChild(style);
    }
    style.textContent = `
        @media (max-width: 600px) {
            html, body { min-width: 0 !important; height: auto !important; min-height: 100% !important; }
            .login_changjiang { min-width: 0 !important; height: auto !important; min-height: 100vh !important; }
            .login_changjiang .wrapper-inner { box-sizing: border-box !important; width: 100% !important; height: auto !important; min-height: 100vh !important; padding: 12px 16px 32px !important; justify-content: flex-start !important; }
            .login_changjiang .server-wrapper { box-sizing: border-box !important; width: 100% !important; padding-bottom: 12px !important; }
            .login_changjiang .L_title { width: 100% !important; font-size: 18px !important; white-space: nowrap !important; justify-content: center !important; }
            .login_changjiang .L_title .logo { flex: 0 0 28px !important; }
            .login_changjiang .L_title .J_host { flex-shrink: 0 !important; }
            .login_changjiang .L_title .download-entry, .login_changjiang .L_title .language { display: none !important; }
            .login_changjiang .login-box { width: 100% !important; max-width: 416px !important; height: auto !important; margin: 16px auto 0 !important; }
            .login_changjiang .mabox { width: 100% !important; height: auto !important; min-height: 377px !important; padding-bottom: 16px !important; }
            .login_changjiang #support-socket { width: 100% !important; height: auto !important; }
            .login_changjiang #qrcode-box { box-sizing: border-box !important; width: min(240px, calc(100vw - 64px)) !important; height: min(240px, calc(100vw - 64px)) !important; margin: 20px auto 16px !important; }
            .login_changjiang #qrcode-box .logma { width: 100% !important; height: 100% !important; }
            .login_changjiang .changeImg { left: auto !important; right: 0 !important; width: 48px !important; height: auto !important; }
            .login_changjiang .way-tips { left: auto !important; right: 54px !important; }
            .login_changjiang .agreement-box { position: static !important; transform: none !important; width: 100% !important; height: auto !important; flex-wrap: wrap !important; margin: 16px auto !important; }
            .login_changjiang .copyright_thu { position: static !important; width: 100% !important; padding-bottom: 16px !important; }
        }
    `;
    window.dispatchEvent(new Event('resize'));
})();
