package com.clipforge.app;

import android.app.Activity;
import android.app.DownloadManager;
import android.content.ContentValues;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.provider.MediaStore;
import android.util.Base64;
import android.view.ViewGroup;
import android.webkit.JavascriptInterface;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.URL;

/**
 * ClipForge Android 壳。
 *
 * 架构：把 Go 后端（clipforge-web/server）交叉编译成 android/arm64 的可执行文件，
 * 以 libclipforge.so 之名打进 APK 的 lib/<abi>/ 下；本 Activity 启动时 exec 它，
 * 待本地 127.0.0.1:8731 就绪后用 WebView 加载其内置的 Web UI。
 * 这样 WebView 里跑的就是完整的 web 版（图片/视频/语音/克隆/账本），功能与 web 版一致。
 */
public class MainActivity extends Activity {

    private static final String BASE = "http://127.0.0.1:8731";
    private static final int REQ_FILE = 1001;
    private static final int REQ_PERM = 1002;

    private WebView web;
    private Process serverProc;
    private ValueCallback<Uri[]> filePathCallback;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        web = new WebView(this);
        // 与 Web UI 深色底一致,避免加载瞬间的白闪
        web.setBackgroundColor(0xFF0B0B14);
        setContentView(web, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setDatabaseEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setAllowFileAccess(true);
        // 按 viewport meta(width=device-width)渲染,启用页面自带的移动端自适应布局
        s.setLoadWithOverviewMode(true);
        s.setUseWideViewPort(true);
        // 壳内不需要手势缩放(页面已自适应),避免误触放大后卡在缩放态
        s.setSupportZoom(false);
        s.setBuiltInZoomControls(false);
        s.setTextZoom(100);
        s.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);

        web.addJavascriptInterface(new Bridge(), "AndroidBridge");

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri u = request.getUrl();
                String host = u.getHost();
                if (host != null && (host.equals("127.0.0.1") || host.equals("localhost"))) {
                    return false; // 本地 UI，正常加载
                }
                // 外部链接（如 DashScope 素材直链）交给系统处理
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, u));
                } catch (Exception ignored) {
                }
                return true;
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                injectDownloadShim(view);
            }
        });

        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> cb,
                                             FileChooserParams params) {
                if (filePathCallback != null) {
                    filePathCallback.onReceiveValue(null);
                }
                filePathCallback = cb;
                try {
                    Intent i = new Intent(Intent.ACTION_GET_CONTENT);
                    i.addCategory(Intent.CATEGORY_OPENABLE);
                    i.setType("*/*");
                    startActivityForResult(Intent.createChooser(i, "选择文件"), REQ_FILE);
                } catch (Exception e) {
                    filePathCallback = null;
                    return false;
                }
                return true;
            }
        });

        // http(s) 直链下载（图片/视频）走系统下载器
        web.setDownloadListener((url, userAgent, contentDisposition, mimetype, contentLength) ->
                downloadViaManager(url, mimetype, guessName(url)));

        requestLegacyStorageIfNeeded();
        startServer();
        waitForServerThenLoad();
    }

    // ==================== Go 后端进程 ====================

    private void startServer() {
        try {
            File bin = new File(getApplicationInfo().nativeLibraryDir, "libclipforge.so");
            File cfg = new File(getFilesDir(), "config");
            cfg.mkdirs();
            ProcessBuilder pb = new ProcessBuilder(bin.getAbsolutePath());
            pb.environment().put("XDG_CONFIG_HOME", cfg.getAbsolutePath());
            pb.environment().put("HOME", getFilesDir().getAbsolutePath());
            pb.environment().put("TMPDIR", getCacheDir().getAbsolutePath());
            String dns = detectDnsServers();
            if (!dns.isEmpty()) {
                pb.environment().put("CLIPFORGE_DNS", dns);
            }
            pb.redirectErrorStream(true);
            serverProc = pb.start();
        } catch (Exception e) {
            toast("后端启动失败：" + e.getMessage());
        }
    }

    /**
     * Android 没有 /etc/resolv.conf，内嵌的 Go 后端取不到 DNS 服务器，
     * 出网请求会全部解析失败（表现为接口返回 502）。
     * 这里从 ConnectivityManager 读出当前网络的 DNS，通过环境变量交给后端。
     */
    private String detectDnsServers() {
        StringBuilder sb = new StringBuilder();
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
                ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
                if (cm != null) {
                    Network n = cm.getActiveNetwork();
                    if (n != null) {
                        LinkProperties lp = cm.getLinkProperties(n);
                        if (lp != null) {
                            for (InetAddress a : lp.getDnsServers()) {
                                String h = a.getHostAddress();
                                if (h != null && !h.isEmpty()) {
                                    if (sb.length() > 0) sb.append(',');
                                    sb.append(h);
                                }
                            }
                        }
                    }
                }
            }
        } catch (Throwable ignored) {
        }
        return sb.toString();
    }

    private void waitForServerThenLoad() {
        new Thread(() -> {
            for (int i = 0; i < 120; i++) {
                if (probe()) {
                    runOnUiThread(() -> web.loadUrl(BASE));
                    return;
                }
                try {
                    Thread.sleep(150);
                } catch (InterruptedException ignored) {
                }
            }
            runOnUiThread(() -> {
                toast("后端未就绪，尝试直接打开…");
                web.loadUrl(BASE);
            });
        }).start();
    }

    private boolean probe() {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(BASE + "/api/config").openConnection();
            c.setConnectTimeout(600);
            c.setReadTimeout(600);
            int code = c.getResponseCode();
            c.disconnect();
            return code == 200;
        } catch (Exception e) {
            return false;
        }
    }

    // ==================== 下载 / 保存 ====================

    private void injectDownloadShim(WebView view) {
        String js = "(function(){if(window.__cfShim)return;window.__cfShim=1;"
                + "document.addEventListener('click',function(e){"
                + "var a=e.target;while(a&&a.tagName!=='A')a=a.parentElement;if(!a)return;"
                + "var href=a.getAttribute('href')||'';"
                + "var isDl=a.hasAttribute('download')||a.getAttribute('target')==='_blank';"
                + "if(!isDl)return;"
                + "if(href.indexOf('http')!==0&&href.indexOf('blob:')!==0)return;"
                + "e.preventDefault();e.stopPropagation();"
                + "var name=a.getAttribute('download')||(href.split('/').pop().split('?')[0])||'clipforge-download';"
                + "if(href.indexOf('blob:')===0){"
                + "fetch(href).then(function(r){return r.blob();}).then(function(b){"
                + "var fr=new FileReader();fr.onload=function(){var s=fr.result;var i=s.indexOf(',');"
                + "AndroidBridge.saveBase64(name,b.type||'application/octet-stream',s.substring(i+1));};"
                + "fr.readAsDataURL(b);}).catch(function(err){AndroidBridge.toastMsg('下载失败: '+err);});"
                + "}else{AndroidBridge.downloadUrl(href,name);}},true);})();";
        view.evaluateJavascript(js, null);
    }

    private String guessName(String url) {
        try {
            String p = Uri.parse(url).getLastPathSegment();
            if (p != null && p.length() > 0) {
                return p;
            }
        } catch (Exception ignored) {
        }
        return "clipforge-" + System.currentTimeMillis();
    }

    private void downloadViaManager(String url, String mime, String name) {
        try {
            DownloadManager.Request r = new DownloadManager.Request(Uri.parse(url));
            r.setTitle(name);
            r.setDescription("ClipForge 下载");
            r.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            r.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
            if (mime != null && mime.length() > 0) {
                r.setMimeType(mime);
            }
            DownloadManager dm = (DownloadManager) getSystemService(DOWNLOAD_SERVICE);
            dm.enqueue(r);
            toast("开始下载：" + name);
        } catch (Exception e) {
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)));
            } catch (Exception ignored) {
                toast("下载失败：" + e.getMessage());
            }
        }
    }

    private String saveToDownloads(String name, String mime, byte[] data) throws Exception {
        if (mime == null || mime.isEmpty()) {
            mime = "application/octet-stream";
        }
        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues cv = new ContentValues();
            cv.put(MediaStore.Downloads.DISPLAY_NAME, name);
            cv.put(MediaStore.Downloads.MIME_TYPE, mime);
            Uri uri = getContentResolver().insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
            if (uri == null) {
                throw new Exception("无法创建下载项");
            }
            OutputStream os = getContentResolver().openOutputStream(uri);
            os.write(data);
            os.flush();
            os.close();
            return name;
        } else {
            File dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
            if (!dir.exists()) {
                dir.mkdirs();
            }
            File f = new File(dir, name);
            FileOutputStream fo = new FileOutputStream(f);
            fo.write(data);
            fo.flush();
            fo.close();
            return f.getAbsolutePath();
        }
    }

    private void requestLegacyStorageIfNeeded() {
        if (Build.VERSION.SDK_INT < 29
                && checkSelfPermission(android.Manifest.permission.WRITE_EXTERNAL_STORAGE)
                != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{android.Manifest.permission.WRITE_EXTERNAL_STORAGE}, REQ_PERM);
        }
    }

    // ==================== JS 桥 ====================

    public class Bridge {
        @JavascriptInterface
        public void saveBase64(String name, String mime, String b64) {
            try {
                byte[] data = Base64.decode(b64, Base64.DEFAULT);
                String saved = saveToDownloads(name, mime, data);
                toast("已保存到「下载」：" + saved);
            } catch (Exception e) {
                toast("保存失败：" + e.getMessage());
            }
        }

        @JavascriptInterface
        public void downloadUrl(String url, String name) {
            downloadViaManager(url, null, name);
        }

        @JavascriptInterface
        public void toastMsg(String msg) {
            toast(msg);
        }
    }

    // ==================== 生命周期 ====================

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        if (requestCode == REQ_FILE) {
            Uri[] result = null;
            if (resultCode == RESULT_OK && data != null) {
                if (data.getDataString() != null) {
                    result = new Uri[]{Uri.parse(data.getDataString())};
                }
            }
            if (filePathCallback != null) {
                filePathCallback.onReceiveValue(result);
                filePathCallback = null;
            }
            return;
        }
        super.onActivityResult(requestCode, resultCode, data);
    }

    @Override
    public void onBackPressed() {
        if (web != null && web.canGoBack()) {
            web.goBack();
        } else {
            super.onBackPressed();
        }
    }

    @Override
    protected void onDestroy() {
        if (serverProc != null) {
            serverProc.destroy();
            serverProc = null;
        }
        if (web != null) {
            web.destroy();
            web = null;
        }
        super.onDestroy();
    }

    private void toast(String msg) {
        runOnUiThread(() -> Toast.makeText(MainActivity.this, msg, Toast.LENGTH_SHORT).show());
    }
}
