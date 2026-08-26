import QtQuick
import qs.Common
import qs.Widgets
import qs.Modules.Plugins

PluginSettings {
    pluginId: "cpUserCard"

    StyledText {
        width: parent.width
        wrapMode: Text.WordWrap
        text: "Data is written to status.json by the cp-notifier daemon; this plugin only reads and displays it. To change accounts, edit the cp-notifier config file."
        font.pixelSize: Theme.fontSizeMedium
        color: Theme.surfaceVariantText
    }

    StringSetting {
        settingKey: "statusPath"
        label: "status.json path"
        description: "Leave empty for the default: (XDG_STATE_HOME or ~/.local/state)/cp-notifier/status.json"
        placeholder: "Leave empty for default path"
        defaultValue: ""
    }

    StringSetting {
        settingKey: "primaryHandle"
        label: "Account shown on the bar"
        description: "With multiple accounts configured, the bar shows this handle; leave empty to show the first one"
        placeholder: "Leave empty for the first account"
        defaultValue: ""
    }

    ToggleSetting {
        settingKey: "showHandle"
        label: "Show handle"
        description: "Also show the handle in the bar pill"
        defaultValue: false
    }

    ToggleSetting {
        settingKey: "showPlatformBadge"
        label: "Show platform badge"
        description: "Show the CF / AC platform badge in the bar pill"
        defaultValue: true
    }
}
