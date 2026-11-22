package link.reminis.app.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp

/**
 * The few icons the base set of Compose (`material-icons-core`) lacks,
 * drawn from the Material Symbols paths (Apache 2.0): no new dependency.
 */
object AppIcons {
    private fun icon(name: String, path: String) = ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f)
        .addPath(addPathNodes(path), fill = SolidColor(Color.Black))
        .build()

    val Folder: ImageVector by lazy {
        icon("Folder", "M10,4H4c-1.1,0 -1.99,0.9 -1.99,2L2,18c0,1.1 0.9,2 2,2h16c1.1,0 2,-0.9 2,-2V8c0,-1.1 -0.9,-2 -2,-2h-8l-2,-2z")
    }

    val File: ImageVector by lazy {
        icon("File", "M6,2c-1.1,0 -1.99,0.9 -1.99,2L4,20c0,1.1 0.89,2 1.99,2H18c1.1,0 2,-0.9 2,-2V8l-6,-6H6zM13,9V3.5L18.5,9H13z")
    }

    /** A pak folder or a zip archive (a box). */
    val Package: ImageVector by lazy {
        icon(
            "Package",
            "M20,2H4c-1,0 -2,0.9 -2,2v3.01c0,0.72 0.43,1.34 1,1.69V20c0,1.1 1.1,2 2,2h14c0.9,0 2,-0.9 2,-2V8.7c0.57,-0.35 1,-0.97 1,-1.69V4c0,-1.1 -1,-2 -2,-2zM15,14H9v-2h6v2zM20,7H4V4l16,-0.02V7z",
        )
    }

    /** Filter and Sort of the Library. */
    val FilterList: ImageVector by lazy { icon("FilterList", "M10,18h4v-2h-4v2zM3,6v2h18L21,6L3,6zM6,13h12v-2L6,11v2z") }

    /** The Library tab (a stack of cards). */
    val Library: ImageVector by lazy {
        icon(
            "Library",
            "M4,6H2v14c0,1.1 0.9,2 2,2h14v-2H4V6zM20,2H8c-1.1,0 -2,0.9 -2,2v12c0,1.1 0.9,2 2,2h12c1.1,0 2,-0.9 2,-2V4c0,-1.1 -0.9,-2 -2,-2zM19,11H9V9h10v2zM15,15H9v-2h6v2zM19,7H9V5h10v2z",
        )
    }

    /** The Console tab (a handheld). */
    val Console: ImageVector by lazy {
        icon(
            "Console",
            "M21,6H3c-1.1,0 -2,0.9 -2,2v8c0,1.1 0.9,2 2,2h18c1.1,0 2,-0.9 2,-2V8c0,-1.1 -0.9,-2 -2,-2zM11,13H8v3H6v-3H3v-2h3V8h2v3h3v2zM15.5,15c-0.83,0 -1.5,-0.67 -1.5,-1.5s0.67,-1.5 1.5,-1.5 1.5,0.67 1.5,1.5 -0.67,1.5 -1.5,1.5zM19.5,12c-0.83,0 -1.5,-0.67 -1.5,-1.5S18.67,9 19.5,9s1.5,0.67 1.5,1.5 -0.67,1.5 -1.5,1.5z",
        )
    }
}
