import QtQuick
import QtCore
import Quickshell
import Quickshell.Io
import qs.Common
import qs.Services
import qs.Widgets
import qs.Modules.Plugins

// CP User Card —— 读取 cp-notifier 写入的 status.json,
// 在 DankBar 显示 rating,点击弹出用户信息卡片。
PluginComponent {
    id: root

    layerNamespacePlugin: "cp-user-card"

    // ---------------------------------------------------------------- 设置(由设置页保存,自动注入)
    readonly property string cfgStatusPath: (pluginData.statusPath || "").trim()
    readonly property string cfgPrimary: (pluginData.primaryHandle || "").trim()
    readonly property bool cfgShowHandle: pluginData.showHandle === true
    readonly property bool cfgShowBadge: pluginData.showPlatformBadge !== false

    readonly property string defaultStatusPath: StandardPaths.writableLocation(StandardPaths.GenericStateLocation) + "/cp-notifier/status.json"
    readonly property string statusPath: cfgStatusPath !== "" ? cfgStatusPath : defaultStatusPath

    // ---------------------------------------------------------------- 数据
    property var accounts: []
    property int updatedAt: 0
    property bool dataAvailable: false
    property int now: Math.floor(Date.now() / 1000)
    property int contentHeightHint: 220
    // 账号翻页: 各卡片实测高度(键 "platform/handle")与翻页器高度(取各页最大值,
    // 翻页时弹层高度不跳动);savedPage 记忆用户停留的页,数据刷新后恢复
    property var pageHeights: ({})
    property int pagerHeight: 0
    property int savedPage: 0

    readonly property var primaryAccount: {
        if (accounts.length === 0)
            return null;
        if (cfgPrimary !== "") {
            for (let i = 0; i < accounts.length; i++) {
                if ((accounts[i].handle || "").toLowerCase() === cfgPrimary.toLowerCase())
                    return accounts[i];
            }
        }
        return accounts[0];
    }
    readonly property int totalPending: {
        let s = 0;
        for (let i = 0; i < accounts.length; i++)
            s += accounts[i].pending || 0;
        return s;
    }

    function parseStatus(txt) {
        try {
            const d = JSON.parse(txt);
            accounts = d.accounts || [];
            updatedAt = d.updated_at || 0;
            dataAvailable = true;
        } catch (e) {
            console.warn("[cpUserCard] status.json 解析失败:", e);
        }
    }

    function timeAgo(ts) {
        if (!ts || ts <= 0)
            return "unknown";
        const d = Math.max(0, now - ts);
        if (d < 60)
            return "just now";
        if (d < 3600)
            return Math.floor(d / 60) + "m ago";
        if (d < 86400)
            return Math.floor(d / 3600) + "h ago";
        return Math.floor(d / 86400) + "d ago";
    }

    function capWords(s) {
        if (!s)
            return "";
        return s.split(" ").map(w => w.length > 0 ? w.charAt(0).toUpperCase() + w.slice(1) : w).join(" ");
    }

    // 失败判定的明确红色: DMS Theme.error 是 Material You 的浅鲑粉
    // (暗色主题默认 #F2B8B5),看起来像粉色,故不用它;改用自定义纯红,
    // 按明/暗主题微调保证可读性,并与绿色 AC(Theme.success)保持区分。
    readonly property color verdictFailColor: Theme.isLightMode ? "#D32F2F" : "#FF5252"

    // 判定结果配色: 失败判定一律明确的红色;成功用 Theme.success,
    // 部分分/评测中/编译错误等用语义色或中性色,明暗主题自适应。
    function verdictColor(v) {
        switch (v) {
        case "OK":
        case "AC":
            return Theme.success;
        // ---- 失败判定: 明确的红(含 CF 原始码与短码两种形式)
        case "WRONG_ANSWER":
        case "WA":
        case "FAILED":
        case "FAIL":
        case "CHALLENGED":
        case "HACK":
        case "TIME_LIMIT_EXCEEDED":
        case "TLE":
        case "MEMORY_LIMIT_EXCEEDED":
        case "MLE":
        case "IDLENESS_LIMIT_EXCEEDED":
        case "ILE":
        case "OLE":
        case "RUNTIME_ERROR":
        case "RE":
        case "CRASHED":
        case "CRASH":
        case "PRESENTATION_ERROR":
        case "PE":
        case "REJECTED":
        case "REJ":
        case "SECURITY_VIOLATION":
        case "SEC":
            return root.verdictFailColor;
        // ---- 其它: 部分分按警告色,评测中按主题色,编译错误/跳过/内部异常用中性灰
        case "PARTIAL":
            return Theme.warning;
        case "TESTING":
        case "WJ":
        case "WR":
            return Theme.primary;
        case "COMPILATION_ERROR":
        case "CE":
        case "SKIPPED":
        case "SKIP":
        case "IE":
            return Theme.surfaceVariantText;
        default:
            return Theme.surfaceVariantText;
        }
    }

    // 最近提交列表: 优先新版 recent_submissions(多条),
    // 旧版守护进程只有单条 last_submission 时回退
    function subList(account) {
        if (!account)
            return [];
        if (account.recent_submissions && account.recent_submissions.length > 0)
            return account.recent_submissions;
        if (account.last_submission)
            return [account.last_submission];
        return [];
    }

    // 提交卡片第二行: 判定说明 + 详情(失败测试点/得分) + 语言 + 相对时间
    // 旧版数据没有 detail 字段时自动跳过
    function subMeta(sub) {
        const parts = [];
        if (sub.label)
            parts.push(sub.label);
        if (sub.detail)
            parts.push(sub.detail);
        if (sub.lang)
            parts.push(sub.lang);
        if (sub.time)
            parts.push(timeAgo(sub.time));
        return parts.join(" · ");
    }

    function pillText() {
        if (!dataAvailable)
            return "CP";
        const a = primaryAccount;
        if (!a)
            return "CP";
        const parts = [];
        if (cfgShowHandle)
            parts.push(a.handle);
        parts.push(a.rating !== undefined && a.rating !== null ? String(a.rating) : "----");
        return parts.join(" ");
    }

    function pillColor() {
        const a = primaryAccount;
        if (a && a.rank_color)
            return a.rank_color;
        return Theme.surfaceText;
    }

    // 翻页器高度 = 当前账号列表中各页已实测高度的最大值(只统计当前账号,
    // 配置删减账号后不受陈旧记录影响);尚无实测值时保持原高度,避免塌陷
    function updatePagerHeight() {
        let m = 0;
        for (let i = 0; i < accounts.length; i++) {
            const a = accounts[i];
            const h = pageHeights[(a.platform || "") + "/" + (a.handle || "")] || 0;
            if (h > m)
                m = h;
        }
        if (m > 0 && m !== pagerHeight)
            pagerHeight = m;
    }

    FileView {
        id: statusFile
        path: root.statusPath
        blockWrites: true
        watchChanges: true
        onFileChanged: reloadDebounce.restart()
        onLoaded: root.parseStatus(text())
        onLoadFailed: {
            root.accounts = [];
            root.dataAvailable = false;
        }
    }

    Timer {
        id: reloadDebounce
        interval: 100
        repeat: false
        onTriggered: statusFile.reload()
    }

    // 相对时间显示的心跳;同时兜底文件监听覆盖不到的场景
    // (例如插件加载时 status.json 尚不存在,inotify 不会为其创建发事件)
    Timer {
        interval: 5000
        running: true
        repeat: true
        onTriggered: {
            root.now = Math.floor(Date.now() / 1000);
            if (!root.dataAvailable || root.now - root.updatedAt > 20)
                statusFile.reload();
        }
    }

    // ---------------------------------------------------------------- 交互
    pillRightClickAction: () => {
        statusFile.reload();
        ToastService.showInfo("CP User Card", "Reloaded status.json");
    }

    // ---------------------------------------------------------------- 横条 pill
    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingXS

            StyledText {
                visible: root.cfgShowBadge
                anchors.verticalCenter: parent.verticalCenter
                text: {
                    const a = root.primaryAccount;
                    if (!a)
                        return "CP";
                    return a.platform === "codeforces" ? "CF" : "AC";
                }
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Bold
                color: Theme.surfaceVariantText
            }

            StyledText {
                anchors.verticalCenter: parent.verticalCenter
                text: root.pillText()
                font.pixelSize: Theme.fontSizeMedium
                font.weight: Font.Medium
                color: root.pillColor()
            }

            StyledText {
                visible: root.totalPending > 0
                anchors.verticalCenter: parent.verticalCenter
                text: "⏳" + root.totalPending
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.primary
            }
        }
    }

    // ---------------------------------------------------------------- 竖条 pill
    verticalBarPill: Component {
        Column {
            spacing: 2

            StyledText {
                anchors.horizontalCenter: parent.horizontalCenter
                visible: root.cfgShowBadge
                text: {
                    const a = root.primaryAccount;
                    if (!a)
                        return "CP";
                    return a.platform === "codeforces" ? "CF" : "AC";
                }
                font.pixelSize: Theme.fontSizeSmall - 2
                font.weight: Font.Bold
                color: Theme.surfaceVariantText
            }

            StyledText {
                anchors.horizontalCenter: parent.horizontalCenter
                text: root.pillText()
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: root.pillColor()
            }

            StyledText {
                visible: root.totalPending > 0
                anchors.horizontalCenter: parent.horizontalCenter
                text: "⏳" + root.totalPending
                font.pixelSize: Theme.fontSizeSmall - 2
                color: Theme.primary
            }
        }
    }

    // ---------------------------------------------------------------- 用户信息卡片弹层
    popoutContent: Component {
        PopoutComponent {
            id: pop

            headerText: "CP Accounts"
            detailsText: root.dataAvailable ?
                "Updated " + root.timeAgo(root.updatedAt) + " · data from cp-notifier" :
                "status.json not found"
            showCloseButton: true

            function syncHeight() {
                root.contentHeightHint = implicitHeight + Theme.spacingL;
            }
            Component.onCompleted: Qt.callLater(syncHeight)
            onImplicitHeightChanged: syncHeight()

            Column {
                width: parent.width
                spacing: Theme.spacingM
                topPadding: Theme.spacingS

                StyledText {
                    visible: !root.dataAvailable
                    width: parent.width
                    wrapMode: Text.WordWrap
                    text: "Could not find " + root.statusPath + "\n\nMake sure the cp-notifier service is running:\nsystemctl --user status cp-notifier"
                    font.pixelSize: Theme.fontSizeMedium
                    color: Theme.surfaceVariantText
                }

                // 账号翻页器: 横向 ListView + SnapOneItem(与 DMS WallpaperTab
                // 相同的分页惯例),一次只显示一个账号卡片,左右拖拽切换;
                // 单账号时禁用交互且不显示指示器,表现与旧版完全一致
                ListView {
                    id: accountPager
                    width: parent.width
                    height: root.pagerHeight
                    orientation: ListView.Horizontal
                    snapMode: ListView.SnapOneItem
                    highlightRangeMode: ListView.StrictlyEnforceRange
                    preferredHighlightBegin: 0
                    preferredHighlightEnd: width
                    highlightMoveDuration: Theme.mediumDuration
                    boundsBehavior: Flickable.StopAtBounds
                    clip: true
                    interactive: root.accounts.length > 1
                    // 预先创建全部页,使每页高度都能实测计入 pageHeights,
                    // 翻页器高度取各页最大值(账号数很少,代价可忽略)
                    cacheBuffer: Math.max(1, root.accounts.length) * width
                    model: root.accounts

                    // 数据刷新(root.accounts 重新赋值)会重建视图,恢复用户停留的页
                    onModelChanged: Qt.callLater(function () {
                        currentIndex = Math.min(root.savedPage, Math.max(0, count - 1));
                    })
                    onMovementEnded: root.savedPage = currentIndex

                    delegate: StyledRect {
                        id: card
                        required property var modelData
                        readonly property var subs: root.subList(modelData)
                        width: accountPager.width
                        height: cardCol.implicitHeight + Theme.spacingM * 2

                        // 实测高度上报(按账号区分),翻页器高度取各页最大值
                        onHeightChanged: {
                            if (!modelData)
                                return;
                            root.pageHeights[(modelData.platform || "") + "/" + (modelData.handle || "")] = height;
                            root.updatePagerHeight();
                        }
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh

                        Column {
                            id: cardCol
                            anchors.left: parent.left
                            anchors.right: parent.right
                            anchors.top: parent.top
                            anchors.margins: Theme.spacingM
                            spacing: Theme.spacingS

                            // 头像 + 用户名 + 段位 + rating
                            Row {
                                width: parent.width
                                spacing: Theme.spacingM

                                Rectangle {
                                    width: 52
                                    height: 52
                                    radius: 26
                                    clip: true
                                    color: Theme.surfaceContainerHighest
                                    anchors.verticalCenter: parent.verticalCenter

                                    Image {
                                        id: avatarImg
                                        anchors.fill: parent
                                        source: card.modelData.avatar || ""
                                        asynchronous: true
                                        fillMode: Image.PreserveAspectCrop
                                    }
                                    DankIcon {
                                        anchors.centerIn: parent
                                        visible: avatarImg.status !== Image.Ready
                                        name: "account_circle"
                                        size: 30
                                        color: Theme.surfaceVariantText
                                    }
                                }

                                Column {
                                    width: parent.width - 52 - Theme.spacingM
                                    anchors.verticalCenter: parent.verticalCenter
                                    spacing: 2

                                    Row {
                                        spacing: Theme.spacingS
                                        StyledText {
                                            text: card.modelData.handle
                                            font.pixelSize: Theme.fontSizeLarge
                                            font.weight: Font.Bold
                                            color: card.modelData.rank_color || Theme.surfaceText
                                        }
                                        StyledText {
                                            anchors.bottom: parent.bottom
                                            anchors.bottomMargin: 2
                                            text: card.modelData.platform === "codeforces" ? "CF" : "AC"
                                            font.pixelSize: Theme.fontSizeSmall
                                            font.weight: Font.Bold
                                            color: Theme.surfaceVariantText
                                        }
                                    }

                                    // 段位 + 当前 rating 按当前分段色;最高 rating 按 max 数值
                                    // 独立分段取色(Go 端 max_rank_color;旧版守护进程无该字段时
                                    // 回退 rank_color,保持旧数据兼容)
                                    Row {
                                        visible: card.modelData.info_ok
                                        spacing: 0

                                        StyledText {
                                            text: root.capWords(card.modelData.rank || "Unrated") +
                                                  (card.modelData.rating !== undefined && card.modelData.rating !== null ?
                                                       " · " + card.modelData.rating : "")
                                            font.pixelSize: Theme.fontSizeMedium
                                            color: card.modelData.rank_color || Theme.surfaceText
                                        }
                                        StyledText {
                                            visible: card.modelData.max_rating !== undefined && card.modelData.max_rating !== null
                                            text: "(max "
                                            font.pixelSize: Theme.fontSizeMedium
                                            color: Theme.surfaceVariantText
                                        }
                                        StyledText {
                                            visible: card.modelData.max_rating !== undefined && card.modelData.max_rating !== null
                                            text: card.modelData.max_rating
                                            font.pixelSize: Theme.fontSizeMedium
                                            font.weight: Font.Medium
                                            color: card.modelData.max_rank_color || card.modelData.rank_color || Theme.surfaceText
                                        }
                                        StyledText {
                                            visible: card.modelData.max_rating !== undefined && card.modelData.max_rating !== null
                                            text: ")"
                                            font.pixelSize: Theme.fontSizeMedium
                                            color: Theme.surfaceVariantText
                                        }
                                    }

                                    StyledText {
                                        visible: !card.modelData.info_ok
                                        text: card.modelData.info_error ?
                                            "User info: " + card.modelData.info_error :
                                            "User info not fetched yet (waiting for cp-notifier)"
                                        font.pixelSize: Theme.fontSizeSmall
                                        color: card.modelData.info_error ? Theme.error : Theme.surfaceVariantText
                                    }
                                }
                            }

                            // CF 附加统计
                            StyledText {
                                visible: card.modelData.info_ok &&
                                    (card.modelData.contribution !== undefined && card.modelData.contribution !== null ||
                                     card.modelData.friend_of_count !== undefined && card.modelData.friend_of_count !== null)
                                width: parent.width
                                elide: Text.ElideRight
                                maximumLineCount: 1
                                text: {
                                    const parts = [];
                                    if (card.modelData.contribution !== undefined && card.modelData.contribution !== null)
                                        parts.push("contrib " + card.modelData.contribution);
                                    if (card.modelData.friend_of_count !== undefined && card.modelData.friend_of_count !== null)
                                        parts.push("fans " + card.modelData.friend_of_count);
                                    if (card.modelData.last_online)
                                        parts.push("last seen " + root.timeAgo(card.modelData.last_online));
                                    return parts.join(" · ");
                                }
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }

                            // 最近比赛
                            StyledText {
                                visible: card.modelData.last_contest !== undefined && card.modelData.last_contest !== null
                                width: parent.width
                                elide: Text.ElideRight
                                maximumLineCount: 1
                                text: {
                                    const c = card.modelData.last_contest;
                                    if (!c)
                                        return "";
                                    let t = "Last contest: " + c.name;
                                    if (c.place)
                                        t += " · rank " + c.place;
                                    if (c.time)
                                        t += " · " + root.timeAgo(c.time);
                                    return t;
                                }
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceVariantText
                            }

                            // 最近提交(卡片式列表: 判定徽标 + 题目 + 详情,点击打开提交页)
                            Column {
                                visible: card.subs.length > 0
                                width: parent.width
                                spacing: Theme.spacingXS

                                StyledText {
                                    text: "Recent submissions"
                                    font.pixelSize: Theme.fontSizeSmall - 1
                                    font.weight: Font.Medium
                                    color: Theme.surfaceVariantText
                                }

                                Repeater {
                                    model: card.subs

                                    delegate: StyledRect {
                                        id: subCard
                                        required property var modelData
                                        readonly property string subURL: modelData.url || ""
                                        readonly property color vColor: root.verdictColor(modelData.verdict || "")

                                        width: parent ? parent.width : 0
                                        height: subRow.implicitHeight + Theme.spacingS * 2
                                        radius: Theme.cornerRadius / 2
                                        color: Theme.surfaceContainerHighest

                                        // 左侧判定色条
                                        Rectangle {
                                            anchors.left: parent.left
                                            anchors.top: parent.top
                                            anchors.bottom: parent.bottom
                                            anchors.leftMargin: 4
                                            anchors.topMargin: 5
                                            anchors.bottomMargin: 5
                                            width: 3
                                            radius: 1.5
                                            color: subCard.vColor
                                        }

                                        Row {
                                            id: subRow
                                            anchors.left: parent.left
                                            anchors.right: parent.right
                                            anchors.verticalCenter: parent.verticalCenter
                                            anchors.leftMargin: 14
                                            anchors.rightMargin: Theme.spacingS
                                            spacing: Theme.spacingS

                                            Column {
                                                anchors.verticalCenter: parent.verticalCenter
                                                // 注意: StyledRect 的 implicitWidth 恒为 0,必须用显式 width 计算
                                                width: subRow.width - verdictChip.width - subRow.spacing
                                                spacing: 1

                                                StyledText {
                                                    width: parent.width
                                                    elide: Text.ElideRight
                                                    maximumLineCount: 1
                                                    text: subCard.modelData.problem || ""
                                                    font.pixelSize: Theme.fontSizeSmall
                                                    font.weight: Font.Medium
                                                    color: Theme.surfaceText
                                                }
                                                StyledText {
                                                    width: parent.width
                                                    elide: Text.ElideRight
                                                    maximumLineCount: 1
                                                    text: root.subMeta(subCard.modelData)
                                                    font.pixelSize: Theme.fontSizeSmall - 1
                                                    color: Theme.surfaceVariantText
                                                }
                                            }

                                            // 判定徽标(短码 chip,代替 emoji 作为主要视觉表达)
                                            StyledRect {
                                                id: verdictChip
                                                anchors.verticalCenter: parent.verticalCenter
                                                width: chipText.implicitWidth + Theme.spacingS * 2
                                                height: chipText.implicitHeight + Theme.spacingXS
                                                radius: height / 2
                                                color: Theme.withAlpha(subCard.vColor, 0.16)

                                                StyledText {
                                                    id: chipText
                                                    anchors.centerIn: parent
                                                    // 旧版守护进程没有 short 字段时回退到完整判定说明
                                                    text: subCard.modelData.short || subCard.modelData.label || ""
                                                    font.pixelSize: Theme.fontSizeSmall - 1
                                                    font.weight: Font.Bold
                                                    color: subCard.vColor
                                                }
                                            }
                                        }

                                        // DMS 风格 hover 反馈;有提交链接时点击打开提交页
                                        StateLayer {
                                            stateColor: Theme.surfaceText
                                            disabled: subCard.subURL === ""
                                            onClicked: {
                                                if (subCard.subURL !== "")
                                                    Qt.openUrlExternally(subCard.subURL);
                                            }
                                        }
                                    }
                                }
                            }

                            // 评测中提示(小胶囊,与判定胶囊同风格)
                            StyledRect {
                                visible: (card.modelData.pending || 0) > 0
                                width: pendText.implicitWidth + Theme.spacingS * 2
                                height: pendText.implicitHeight + Theme.spacingXS
                                radius: height / 2
                                color: Theme.withAlpha(Theme.primary, 0.16)

                                StyledText {
                                    id: pendText
                                    anchors.centerIn: parent
                                    text: "⏳ " + card.modelData.pending + " judging"
                                    font.pixelSize: Theme.fontSizeSmall - 1
                                    font.weight: Font.Medium
                                    color: Theme.primary
                                }
                            }

                            // 打开主页
                            StyledText {
                                text: "Open profile →"
                                font.pixelSize: Theme.fontSizeSmall
                                color: openProfileArea.containsMouse ? Theme.primary : Theme.surfaceVariantText

                                MouseArea {
                                    id: openProfileArea
                                    anchors.fill: parent
                                    hoverEnabled: true
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: Qt.openUrlExternally(card.modelData.profile_url)
                                }
                            }
                        }
                    }
                }

                // 页面指示器: 小圆点(仅多账号显示),高亮当前页,点击直接跳页
                Row {
                    visible: root.accounts.length > 1
                    anchors.horizontalCenter: parent.horizontalCenter
                    spacing: Theme.spacingXS

                    Repeater {
                        model: root.accounts.length

                        delegate: Rectangle {
                            required property int index
                            width: 6
                            height: 6
                            radius: 3
                            color: accountPager.currentIndex === index ? Theme.primary : Theme.withAlpha(Theme.surfaceVariantText, 0.35)

                            MouseArea {
                                anchors.fill: parent
                                cursorShape: Qt.PointingHandCursor
                                onClicked: {
                                    root.savedPage = index;
                                    accountPager.currentIndex = index;
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    popoutWidth: 420
    popoutHeight: Math.min(760, Math.max(200, root.contentHeightHint))
}
