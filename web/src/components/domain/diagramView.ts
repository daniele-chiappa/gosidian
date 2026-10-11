// plancia-diagrams, its CSS and the drawing libraries behind it (Vue Flow,
// dagre, and Mermaid for the kinds it draws) load the first time a note
// shows a diagram, or a diagram is edited or made: a chunk of their own,
// out of the app's first load.
import 'plancia-diagrams/style.css'

export {
  DiagramView,
  DiagramEditor,
  findDiagramBlocks,
  replaceDiagramBlock,
  writeDiagramBlock,
  findTwin,
  twinText,
  diagramTemplate,
  readyTemplates,
  parseDiagram,
  toMermaid,
  fromMermaid,
  toCanvas,
  fromCanvas,
  PipelineEditor,
  PipelineView,
  readPipeline,
} from 'plancia-diagrams'
